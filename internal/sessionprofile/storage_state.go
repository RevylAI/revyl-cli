package sessionprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// Cookie-file validation follows the BrowserStorageState model in
// cognisim_schemas/cognisim_schemas/schemas/session_profile.py.

type outputFile struct {
	envName string
}

const maxBrowserStorageStateBytes = 1 << 20

var (
	ipvFutureHostPattern = regexp.MustCompile(`\Av[a-fA-F0-9]+\..+\z`)
	utf8ByteOrderMark    = []byte{0xEF, 0xBB, 0xBF}
)

func invalidOutput(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}

func invalidLine(file outputFile, line int, format string, args ...any) error {
	return invalidOutput("%s line %d: %s", file.envName, line, fmt.Sprintf(format, args...))
}

type splitURL struct {
	scheme, netloc, path, query, fragment string
}

func splitURLScheme(value string) (scheme, rest string) {
	value = strings.TrimLeftFunc(value, func(character rune) bool { return character <= ' ' })
	value = strings.NewReplacer("\t", "", "\r", "", "\n", "").Replace(value)
	index := strings.Index(value, ":")
	if index <= 0 || !isASCIILetter(rune(value[0])) {
		return "", value
	}
	for _, character := range value[:index] {
		if !isASCIILetter(character) && !(character >= '0' && character <= '9') && !strings.ContainsRune("+-.", character) {
			return "", value
		}
	}
	return strings.ToLower(value[:index]), value[index+1:]
}

// urlSplit reports false where urlsplit raises ValueError, which it does for
// an authority with unbalanced or misplaced brackets, a bracketed host that
// isn't an IPv6 address or IPvFuture, or characters NFKC turns into URL
// delimiters.
func urlSplit(value string) (splitURL, bool) {
	var parts splitURL
	parts.scheme, value = splitURLScheme(value)
	if strings.HasPrefix(value, "//") {
		end := len(value)
		for _, delimiter := range "/?#" {
			if index := strings.IndexRune(value[2:], delimiter); index >= 0 {
				end = min(end, index+2)
			}
		}
		parts.netloc, value = value[2:end], value[end:]
		if !isSplittableNetloc(parts.netloc) {
			return splitURL{}, false
		}
	}
	value, parts.fragment, _ = strings.Cut(value, "#")
	parts.path, parts.query, _ = strings.Cut(value, "?")
	return parts, true
}

// isSplittableNetloc mirrors the checks of CPython 3.12's urlsplit,
// _check_bracketed_netloc and _checknetloc.
func isSplittableNetloc(netloc string) bool {
	hasOpenBracket, hasCloseBracket := strings.Contains(netloc, "["), strings.Contains(netloc, "]")
	if hasOpenBracket != hasCloseBracket {
		return false
	}
	if hasOpenBracket && !isValidBracketedNetloc(netloc) {
		return false
	}
	if isASCII(netloc) {
		return true
	}
	stripped := strings.NewReplacer("@", "", ":", "", "#", "", "?", "").Replace(netloc)
	normalized := norm.NFKC.String(stripped)
	return normalized == stripped || !strings.ContainsAny(normalized, "/?#@:")
}

func isValidBracketedNetloc(netloc string) bool {
	hostAndPort := netloc[strings.LastIndex(netloc, "@")+1:]
	beforeBracket, bracketed, hasOpenBracket := strings.Cut(hostAndPort, "[")
	hostname := hostAndPort
	if hasOpenBracket {
		if beforeBracket != "" {
			return false
		}
		var port string
		hostname, port, _ = strings.Cut(bracketed, "]")
		if port != "" && !strings.HasPrefix(port, ":") {
			return false
		}
	} else {
		hostname, _, _ = strings.Cut(hostAndPort, ":")
	}
	if strings.HasPrefix(hostname, "v") {
		return ipvFutureHostPattern.MatchString(hostname)
	}
	address, err := netip.ParseAddr(hostname)
	return err == nil && address.Is6() && !strings.Contains(address.Zone(), "%")
}

func isASCII(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] >= utf8.RuneSelf {
			return false
		}
	}
	return true
}

func isASCIILetter(character rune) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

// jsonObject keeps a JSON object's keys in document order, as Python's
// json.loads does, since the first validation error follows that order. A
// repeated key keeps its first position and its last value.
type jsonObject []jsonMember

type jsonMember struct {
	key   string
	value any
}

func (object jsonObject) lookup(key string) (any, bool) {
	for _, member := range object {
		if member.key == key {
			return member.value, true
		}
	}
	return nil, false
}

func decodeOrderedJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return token, nil
	}
	if delimiter == '[' {
		list := []any{}
		for decoder.More() {
			item, err := decodeOrderedJSON(decoder)
			if err != nil {
				return nil, err
			}
			list = append(list, item)
		}
		_, err := decoder.Token()
		return list, err
	}
	object := jsonObject{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		key, _ := keyToken.(string)
		value, err := decodeOrderedJSON(decoder)
		if err != nil {
			return nil, err
		}
		if index := slices.IndexFunc(object, func(member jsonMember) bool { return member.key == key }); index >= 0 {
			object[index].value = value
		} else {
			object = append(object, jsonMember{key: key, value: value})
		}
	}
	_, err = decoder.Token()
	return object, err
}

// parseBrowserStorageState returns canonical state and reports failures without
// echoing cookie or localStorage values.
func parseBrowserStorageState(file outputFile, text string) (json.RawMessage, error) {
	data := []byte(text)
	var raw json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, storageStateSyntaxError(file, data, err)
	}
	if line, found := unpairedSurrogateEscapeLine(data); found {
		return nil, invalidLine(file, line, "contains an unpaired UTF-16 surrogate escape")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	document, err := decodeOrderedJSON(decoder)
	if err != nil {
		return nil, invalidLine(file, 1, "is not valid JSON")
	}
	state, issue := validateStorageState(document)
	if issue != nil {
		return nil, issue.err(file)
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(state); err != nil {
		return nil, invalidOutput("%s: could not be encoded", file.envName)
	}
	canonical := bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))
	if pythonJSONSize(canonical) > maxBrowserStorageStateBytes {
		return nil, invalidOutput("%s: Browser storage state is larger than %d bytes", file.envName, maxBrowserStorageStateBytes)
	}
	return json.RawMessage(canonical), nil
}

// storageStateSyntaxError names the line Python's json.loads stops at, or the
// NaN, Infinity or -Infinity constant it refuses.
func storageStateSyntaxError(file outputFile, data []byte, err error) error {
	position := len(data)
	var syntaxErr *json.SyntaxError
	if errors.As(err, &syntaxErr) && syntaxErr.Offset > 0 && syntaxErr.Offset <= int64(len(data)) && err.Error() != "unexpected end of JSON input" {
		position = int(syntaxErr.Offset) - 1
		message := err.Error()
		if strings.HasSuffix(message, "looking for beginning of value") {
			for _, constant := range []string{"NaN", "Infinity"} {
				if bytes.HasPrefix(data[position:], []byte(constant)) {
					return invalidOutput("%s: %s is not valid JSON", file.envName, constant)
				}
			}
		}
		if message == "invalid character 'I' in numeric literal" && bytes.HasPrefix(data[position-1:], []byte("-Infinity")) {
			return invalidOutput("%s: -Infinity is not valid JSON", file.envName)
		}
	}
	return invalidLine(file, bytes.Count(data[:position], []byte("\n"))+1, "is not valid JSON")
}

// unpairedSurrogateEscapeLine finds the line of the first \uD800-\uDFFF
// escape in valid JSON that isn't a high surrogate followed by a low one. Go's
// decoder would turn it into U+FFFD, a different value, and Python keeps a
// code point UTF-8 can't encode.
func unpairedSurrogateEscapeLine(data []byte) (int, bool) {
	unicodeEscapeAt := func(offset int) (rune, bool) {
		if offset+6 > len(data) || data[offset] != '\\' || data[offset+1] != 'u' {
			return 0, false
		}
		unit, err := strconv.ParseUint(string(data[offset+2:offset+6]), 16, 16)
		return rune(unit), err == nil
	}
	for offset := 0; offset < len(data); offset++ {
		if data[offset] != '\\' {
			continue
		}
		unit, isUnicodeEscape := unicodeEscapeAt(offset)
		if !isUnicodeEscape {
			offset++
			continue
		}
		start := offset
		offset += 5
		if !utf16.IsSurrogate(unit) {
			continue
		}
		if low, isLowEscape := unicodeEscapeAt(offset + 1); isLowEscape && unit < 0xDC00 && low >= 0xDC00 && low <= 0xDFFF {
			offset += 6
			continue
		}
		return bytes.Count(data[:start], []byte("\n")) + 1, true
	}
	return 0, false
}

// storageStateIssue records the first local validation failure by field or reason.
type storageStateIssue struct {
	location []any
	reason   string
}

func (issue *storageStateIssue) err(file outputFile) error {
	if issue.reason != "" {
		return invalidOutput("%s: %s", file.envName, issue.reason)
	}
	var location strings.Builder
	for _, part := range issue.location {
		if index, isIndex := part.(int); isIndex {
			fmt.Fprintf(&location, "[%d]", index)
		} else {
			fmt.Fprintf(&location, ".%s", part)
		}
	}
	if location.Len() == 0 {
		return invalidOutput("%s: is not Playwright storage state", file.envName)
	}
	return invalidOutput("%s: is not Playwright storage state (at %s)", file.envName, strings.TrimPrefix(location.String(), "."))
}

func appendLocation(location []any, part any) []any {
	return append(slices.Clip(location), part)
}

// modelField is one field of a pydantic model. Models that populate by name
// accept the field name where the alias is missing.
type modelField struct {
	alias    string
	name     string
	required bool
	validate func(value any, location []any) *storageStateIssue
}

// validateModel checks fields in declaration order, then refuses keys no
// field used, as pydantic does with extra="forbid".
func validateModel(value any, location []any, fields []modelField) *storageStateIssue {
	object, isObject := value.(jsonObject)
	if !isObject {
		return &storageStateIssue{location: location}
	}
	used := map[string]bool{}
	for _, field := range fields {
		key, fieldValue, found := field.alias, any(nil), false
		if fieldValue, found = object.lookup(field.alias); !found && field.name != "" {
			key = field.name
			fieldValue, found = object.lookup(field.name)
		}
		if !found {
			if field.required {
				return &storageStateIssue{location: appendLocation(location, field.alias)}
			}
			continue
		}
		used[key] = true
		if issue := field.validate(fieldValue, appendLocation(location, key)); issue != nil {
			return issue
		}
	}
	for _, member := range object {
		if !used[member.key] {
			return &storageStateIssue{location: appendLocation(location, member.key)}
		}
	}
	return nil
}

func validateList(value any, location []any, validateItem func(item any, location []any) *storageStateIssue) *storageStateIssue {
	list, isList := value.([]any)
	if !isList {
		return &storageStateIssue{location: location}
	}
	for index, item := range list {
		if issue := validateItem(item, appendLocation(location, index)); issue != nil {
			return issue
		}
	}
	return nil
}

func stringField(target *string, minimum, maximum int) func(any, []any) *storageStateIssue {
	return func(value any, location []any) *storageStateIssue {
		text, isText := value.(string)
		if !isText {
			return &storageStateIssue{location: location}
		}
		if length := utf8.RuneCountInString(text); length < minimum || (maximum > 0 && length > maximum) {
			return &storageStateIssue{location: location}
		}
		*target = text
		return nil
	}
}

// laxFloat converts what pydantic's lax float accepts: numbers, booleans and
// numeric strings.
func laxFloat(value any) (float64, bool) {
	switch value := value.(type) {
	case bool:
		if value {
			return 1, true
		}
		return 0, true
	case json.Number:
		number, err := strconv.ParseFloat(string(value), 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, false
		}
		if !strings.ContainsAny(string(value), ".eE") {
			if math.IsInf(number, 0) {
				return 0, false
			}
			return number + 0, true
		}
		return number, true
	case string:
		candidate := strings.TrimSpace(value)
		if strings.Contains(value, "_") {
			if strings.HasPrefix(value, "_") || strings.HasSuffix(value, "_") || strings.Contains(value, "__") {
				return 0, false
			}
			candidate = strings.ReplaceAll(value, "_", "")
		}
		if strings.ContainsAny(candidate, "xX") {
			return 0, false
		}
		number, err := strconv.ParseFloat(candidate, 64)
		if err != nil && !errors.Is(err, strconv.ErrRange) {
			return 0, false
		}
		return number, true
	}
	return 0, false
}

// laxBool converts what pydantic's lax bool accepts.
func laxBool(value any) (bool, bool) {
	switch value := value.(type) {
	case bool:
		return value, true
	case json.Number:
		if number, err := strconv.ParseFloat(string(value), 64); err == nil && (number == 0 || number == 1) {
			return number == 1, true
		}
	case string:
		switch strings.ToLower(value) {
		case "1", "on", "t", "true", "y", "yes":
			return true, true
		case "0", "off", "f", "false", "n", "no":
			return false, true
		}
	}
	return false, false
}

// canonicalCookie is BrowserCookie as cognisim_schemas serializes it, defaults
// filled, so the size check measures what the backend measures.
type canonicalCookie struct {
	Name         string          `json:"name"`
	Value        string          `json:"value"`
	Domain       string          `json:"domain"`
	Path         string          `json:"path"`
	Expires      json.RawMessage `json:"expires"`
	HTTPOnly     bool            `json:"httpOnly"`
	Secure       bool            `json:"secure"`
	SameSite     string          `json:"sameSite"`
	PartitionKey *string         `json:"partitionKey,omitempty"`
}

type canonicalOrigin struct {
	Origin       string                      `json:"origin"`
	LocalStorage []canonicalLocalStorageItem `json:"localStorage"`
}

type canonicalLocalStorageItem struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type canonicalStorageState struct {
	Cookies []canonicalCookie `json:"cookies"`
	Origins []canonicalOrigin `json:"origins"`
}

func validateStorageState(document any) (canonicalStorageState, *storageStateIssue) {
	state := canonicalStorageState{Cookies: []canonicalCookie{}, Origins: []canonicalOrigin{}}
	issue := validateModel(document, nil, []modelField{
		{alias: "cookies", required: false, validate: func(value any, location []any) *storageStateIssue {
			return validateList(value, location, func(item any, location []any) *storageStateIssue {
				cookie, issue := validateCookie(item, location)
				state.Cookies = append(state.Cookies, cookie)
				return issue
			})
		}},
		{alias: "origins", required: false, validate: func(value any, location []any) *storageStateIssue {
			return validateList(value, location, func(item any, location []any) *storageStateIssue {
				origin, issue := validateOrigin(item, location)
				state.Origins = append(state.Origins, origin)
				return issue
			})
		}},
	})
	return state, issue
}

func validateCookie(value any, location []any) (canonicalCookie, *storageStateIssue) {
	cookie := canonicalCookie{Path: "/", Expires: json.RawMessage("-1.0"), SameSite: "Lax"}
	issue := validateModel(value, location, []modelField{
		{alias: "name", required: true, validate: stringField(&cookie.Name, 1, 4096)},
		{alias: "value", required: true, validate: stringField(&cookie.Value, 0, 16384)},
		{alias: "domain", required: true, validate: stringField(&cookie.Domain, 1, 253)},
		{alias: "path", validate: stringField(&cookie.Path, 1, 2048)},
		{alias: "expires", validate: func(value any, location []any) *storageStateIssue {
			expires, ok := laxFloat(value)
			if !ok || math.IsInf(expires, 0) || math.IsNaN(expires) {
				return &storageStateIssue{location: location}
			}
			cookie.Expires = json.RawMessage(pythonFloatRepr(expires))
			return nil
		}},
		{alias: "httpOnly", name: "http_only", validate: func(value any, location []any) *storageStateIssue {
			var ok bool
			if cookie.HTTPOnly, ok = laxBool(value); !ok {
				return &storageStateIssue{location: location}
			}
			return nil
		}},
		{alias: "secure", validate: func(value any, location []any) *storageStateIssue {
			var ok bool
			if cookie.Secure, ok = laxBool(value); !ok {
				return &storageStateIssue{location: location}
			}
			return nil
		}},
		{alias: "sameSite", name: "same_site", validate: func(value any, location []any) *storageStateIssue {
			sameSite, isText := value.(string)
			if !isText || !slices.Contains([]string{"Strict", "Lax", "None"}, sameSite) {
				return &storageStateIssue{location: location}
			}
			cookie.SameSite = sameSite
			return nil
		}},
		{alias: "partitionKey", name: "partition_key", validate: func(value any, location []any) *storageStateIssue {
			if value == nil {
				cookie.PartitionKey = nil
				return nil
			}
			var partitionKey string
			if issue := stringField(&partitionKey, 0, 2048)(value, location); issue != nil {
				return issue
			}
			cookie.PartitionKey = &partitionKey
			return nil
		}},
		{alias: "_crHasCrossSiteAncestor", name: "chromium_has_cross_site_ancestor", validate: func(value any, location []any) *storageStateIssue {
			if _, ok := laxBool(value); value != nil && !ok {
				return &storageStateIssue{location: location}
			}
			return nil
		}},
	})
	return cookie, issue
}

func validateOrigin(value any, location []any) (canonicalOrigin, *storageStateIssue) {
	origin := canonicalOrigin{LocalStorage: []canonicalLocalStorageItem{}}
	issue := validateModel(value, location, []modelField{
		{alias: "origin", required: true, validate: stringField(&origin.Origin, 1, 2048)},
		{alias: "localStorage", name: "local_storage", validate: func(value any, location []any) *storageStateIssue {
			return validateList(value, location, func(item any, location []any) *storageStateIssue {
				var entry canonicalLocalStorageItem
				issue := validateModel(item, location, []modelField{
					{alias: "name", required: true, validate: stringField(&entry.Name, 1, 4096)},
					{alias: "value", required: true, validate: stringField(&entry.Value, 0, 0)},
				})
				origin.LocalStorage = append(origin.LocalStorage, entry)
				return issue
			})
		}},
	})
	if issue != nil {
		return origin, issue
	}
	parts, splits := urlSplit(origin.Origin)
	if !splits || (parts.scheme != "http" && parts.scheme != "https") || parts.netloc == "" || (parts.path != "" && parts.path != "/") || parts.query != "" || parts.fragment != "" {
		return origin, &storageStateIssue{location: location, reason: "Storage-state origins are http(s) origins with no path"}
	}
	return origin, nil
}

// pythonFloatRepr formats a float the way Python's repr does, which is how
// json.dumps writes it.
func pythonFloatRepr(value float64) string {
	if value == 0 {
		if math.Signbit(value) {
			return "-0.0"
		}
		return "0.0"
	}
	mantissa, exponentText, _ := strings.Cut(strconv.FormatFloat(value, 'e', -1, 64), "e")
	exponent, _ := strconv.Atoi(exponentText)
	sign := ""
	if strings.HasPrefix(mantissa, "-") {
		sign, mantissa = "-", mantissa[1:]
	}
	digits := strings.Replace(mantissa, ".", "", 1)
	switch {
	case exponent < -4 || exponent >= 16:
		formatted := digits[:1]
		if len(digits) > 1 {
			formatted += "." + digits[1:]
		}
		exponentSign := "+"
		if exponent < 0 {
			exponentSign, exponent = "-", -exponent
		}
		return fmt.Sprintf("%s%se%s%02d", sign, formatted, exponentSign, exponent)
	case exponent < 0:
		return sign + "0." + strings.Repeat("0", -exponent-1) + digits
	case len(digits) <= exponent+1:
		return sign + digits + strings.Repeat("0", exponent+1-len(digits)) + ".0"
	}
	return sign + digits[:exponent+1] + "." + digits[exponent+1:]
}

// pythonJSONSize is the length of Go-encoded JSON once written the way
// Python's json.dumps writes it by default: every character outside
// printable ASCII as \uXXXX escapes.
func pythonJSONSize(encoded []byte) int {
	size := 0
	for _, character := range string(encoded) {
		switch {
		case character == 0x7F:
			size += 6
		case character < utf8.RuneSelf:
			size++
		case character > 0xFFFF:
			size += 12
		default:
			size += 6
		}
	}
	return size
}
