package sessionprofile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/revyl/cli/internal/api"
)

// The parser mirrors parse_session_profile_output_files in
// cognisim_schemas/cognisim_schemas/schemas/session_profile_output_files.py
// rule for rule and message for message, so a profile fails the same way
// whether its script runs here or on the hosted runner.

// outputFile is one of the seven optional files a profile script may write.
// kind names it in the session_profile_output_kinds analytics property.
type outputFile struct {
	kind     string
	envName  string
	fileName string
}

var (
	launchEnvFile           = outputFile{"launch_env", "REVYL_LAUNCH_ENV_FILE", "launch-env"}
	secretLaunchEnvFile     = outputFile{"secret_launch_env", "REVYL_SECRET_LAUNCH_ENV_FILE", "secret-launch-env"}
	iosLaunchArgumentsFile  = outputFile{"ios_launch_arguments", "REVYL_IOS_LAUNCH_ARGUMENTS_FILE", "ios-launch-arguments"}
	browserStorageStateFile = outputFile{"browser_storage_state", "REVYL_BROWSER_STORAGE_STATE_FILE", "browser-storage-state.json"}
	deepLinkFile            = outputFile{"deep_link", "REVYL_DEEP_LINK_FILE", "deep-link"}
	variablesFile           = outputFile{"variables", "REVYL_VARIABLES_FILE", "variables"}
	secretVariablesFile     = outputFile{"secret_variables", "REVYL_SECRET_VARIABLES_FILE", "secret-variables"}

	outputFiles = []outputFile{launchEnvFile, secretLaunchEnvFile, iosLaunchArgumentsFile, browserStorageStateFile, deepLinkFile, variablesFile, secretVariablesFile}
)

// Limits shared with SessionProfileOutputs and the web launch env rules in
// cognisim_schemas.
const (
	maxOutputFileBytes          = 1 << 20
	maxOutputKeyLength          = 256
	maxLaunchEnvKeys            = 128
	maxLaunchEnvValueBytes      = 32 << 10
	maxLaunchEnvTotalBytes      = 256 << 10
	maxVariablesKeys            = 256
	maxVariablesTotalBytes      = 256 << 10
	maxIOSLaunchArguments       = 256
	maxIOSLaunchArgumentsBytes  = 64 << 10
	maxDeepLinkBytes            = 8 << 10
	maxBrowserStorageStateBytes = 1 << 20
	maxWebLaunchEnvKeys         = 32
	maxWebLaunchEnvValueBytes   = 4096
	maxWebLaunchEnvTotalBytes   = 128 << 10
)

const (
	heredocMarker = "<<"
	// delimiterWhitespace is what Python's str.strip removes when the parser
	// checks for a blank delimiter.
	delimiterWhitespace = " \t\r\n\v\f"
)

var (
	outputKeyPattern         = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	ipvFutureHostPattern     = regexp.MustCompile(`\Av[a-fA-F0-9]+\..+\z`)
	webLaunchEnvKeyPattern   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,127}$`)
	reservedOutputKeyPrefix  = []string{"DYLD_", "SIMCTL_CHILD_", "COGNISIM_"}
	reservedWebLaunchEnvKeys = []string{"PATH", "HOME", "LANG", "PORT", "HOSTNAME", "WEB_APP_BOOTSTRAP"}
	utf8ByteOrderMark        = []byte{0xEF, 0xBB, 0xBF}
)

// outputTarget is what the session the outputs are for can take.
type outputTarget struct {
	platform            string
	webAcceptsLaunchEnv bool
}

type keyValue struct {
	key   string
	value string
}

func invalidOutput(format string, args ...any) error {
	return &Error{Outcome: OutcomeOutputInvalid, Reason: fmt.Sprintf(format, args...)}
}

func invalidLine(file outputFile, line int, format string, args ...any) error {
	return invalidOutput("%s line %d: %s", file.envName, line, fmt.Sprintf(format, args...))
}

// parseOutputs reads and validates every output file the script wrote.
func parseOutputs(outputDir string, target outputTarget) (api.AppliedSessionProfileOutputs, error) {
	texts := map[string]string{}
	for _, file := range outputFiles {
		text, err := readOutputText(outputDir, file)
		if err != nil {
			return api.AppliedSessionProfileOutputs{}, err
		}
		if text != "" {
			texts[file.kind] = text
		}
	}
	outputs, err := parseOutputTexts(texts, target.platform)
	if err != nil {
		return api.AppliedSessionProfileOutputs{}, err
	}
	if target.platform == "web" && len(outputs.LaunchEnv)+len(outputs.SecretLaunchEnv) > 0 {
		if err := checkWebLaunchEnv(target, outputs.LaunchEnv, outputs.SecretLaunchEnv); err != nil {
			return api.AppliedSessionProfileOutputs{}, err
		}
	}
	return outputs, nil
}

// parseOutputTexts parses the decoded, non-empty output files by kind and
// validates them for the platform.
func parseOutputTexts(texts map[string]string, platform string) (api.AppliedSessionProfileOutputs, error) {
	var outputs api.AppliedSessionProfileOutputs
	linesOf := func(file outputFile) []string {
		text, ok := texts[file.kind]
		if !ok {
			return nil
		}
		return splitOutputLines(text)
	}

	launchEnv, err := parseKeyValues(launchEnvFile, linesOf(launchEnvFile), nil)
	if err != nil {
		return outputs, err
	}
	secretLaunchEnv, err := parseKeyValues(secretLaunchEnvFile, linesOf(secretLaunchEnvFile), &publicKeyValues{launchEnvFile, launchEnv})
	if err != nil {
		return outputs, err
	}
	variables, err := parseKeyValues(variablesFile, linesOf(variablesFile), nil)
	if err != nil {
		return outputs, err
	}
	secretVariables, err := parseKeyValues(secretVariablesFile, linesOf(secretVariablesFile), &publicKeyValues{variablesFile, variables})
	if err != nil {
		return outputs, err
	}
	arguments, err := parseArguments(linesOf(iosLaunchArgumentsFile))
	if err != nil {
		return outputs, err
	}
	var deepLink *string
	if _, ok := texts[deepLinkFile.kind]; ok {
		link, err := parseDeepLink(linesOf(deepLinkFile))
		if err != nil {
			return outputs, err
		}
		deepLink = &link
	}
	var storageState json.RawMessage
	if text, ok := texts[browserStorageStateFile.kind]; ok {
		if storageState, err = parseBrowserStorageState(browserStorageStateFile, text); err != nil {
			return outputs, err
		}
	}

	if err := checkKeyValueLimits(launchEnvFile, secretLaunchEnvFile, launchEnv, secretLaunchEnv, maxLaunchEnvKeys, maxLaunchEnvValueBytes, maxLaunchEnvTotalBytes); err != nil {
		return outputs, err
	}
	if err := checkKeyValueLimits(variablesFile, secretVariablesFile, variables, secretVariables, maxVariablesKeys, 0, maxVariablesTotalBytes); err != nil {
		return outputs, err
	}
	if len(arguments) > maxIOSLaunchArguments {
		return outputs, invalidOutput("%s holds more than %d arguments", iosLaunchArgumentsFile.envName, maxIOSLaunchArguments)
	}
	argumentsBytes := 0
	for _, argument := range arguments {
		argumentsBytes += len(argument)
	}
	if argumentsBytes > maxIOSLaunchArgumentsBytes {
		return outputs, invalidOutput("%s holds more than %d bytes", iosLaunchArgumentsFile.envName, maxIOSLaunchArgumentsBytes)
	}
	if deepLink != nil {
		if *deepLink == "" || strings.ContainsAny(*deepLink, "\x00\r\n") {
			return outputs, invalidOutput("%s must hold one non-empty line", deepLinkFile.envName)
		}
		if len(*deepLink) > maxDeepLinkBytes {
			return outputs, invalidOutput("%s is longer than %d bytes", deepLinkFile.envName, maxDeepLinkBytes)
		}
	}

	if platform != "ios" && len(arguments) > 0 {
		return outputs, invalidOutput("%s applies only to iOS sessions, not %s", iosLaunchArgumentsFile.envName, platform)
	}
	if platform != "web" && storageState != nil {
		return outputs, invalidOutput("%s applies only to web sessions, not %s", browserStorageStateFile.envName, platform)
	}
	if deepLink != nil {
		if err := checkDeepLinkForPlatform(*deepLink, platform); err != nil {
			return outputs, err
		}
	}

	outputs.LaunchEnv, outputs.SecretLaunchEnv = keyValueMap(launchEnv), keyValueMap(secretLaunchEnv)
	outputs.Variables, outputs.SecretVariables = keyValueMap(variables), keyValueMap(secretVariables)
	outputs.IOSLaunchArguments = arguments
	outputs.DeepLink = deepLink
	outputs.BrowserStorageState = storageState
	return outputs, nil
}

// outputKinds lists the kinds of output present, in file order.
func outputKinds(outputs api.AppliedSessionProfileOutputs) []string {
	present := map[string]bool{
		launchEnvFile.kind:           len(outputs.LaunchEnv) > 0,
		secretLaunchEnvFile.kind:     len(outputs.SecretLaunchEnv) > 0,
		iosLaunchArgumentsFile.kind:  len(outputs.IOSLaunchArguments) > 0,
		browserStorageStateFile.kind: outputs.BrowserStorageState != nil,
		deepLinkFile.kind:            outputs.DeepLink != nil,
		variablesFile.kind:           len(outputs.Variables) > 0,
		secretVariablesFile.kind:     len(outputs.SecretVariables) > 0,
	}
	kinds := []string{}
	for _, file := range outputFiles {
		if present[file.kind] {
			kinds = append(kinds, file.kind)
		}
	}
	return kinds
}

// readOutputText returns a file's text after any leading BOM, or "" when the
// file is missing or empty.
func readOutputText(outputDir string, file outputFile) (string, error) {
	path := filepath.Join(outputDir, file.fileName)
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", invalidOutput("%s: is not a readable regular file", file.envName)
	}
	handle, err := os.Open(path) // #nosec G304 -- a fixed output file name inside this run's own output directory
	if err != nil {
		return "", invalidOutput("%s: is not a readable regular file", file.envName)
	}
	defer handle.Close()
	data, err := io.ReadAll(io.LimitReader(handle, maxOutputFileBytes+1))
	if err != nil {
		return "", invalidOutput("%s: could not be read", file.envName)
	}
	if len(data) > maxOutputFileBytes {
		return "", invalidOutput("%s is larger than %d bytes", file.envName, maxOutputFileBytes)
	}
	data = bytes.TrimPrefix(data, utf8ByteOrderMark)
	for offset := 0; offset < len(data); {
		character, size := utf8.DecodeRune(data[offset:])
		if character == utf8.RuneError && size == 1 {
			return "", invalidLine(file, bytes.Count(data[:offset], []byte("\n"))+1, "is not valid UTF-8")
		}
		offset += size
	}
	if index := bytes.IndexByte(data, 0); index >= 0 {
		return "", invalidLine(file, bytes.Count(data[:index], []byte("\n"))+1, "contains NUL")
	}
	return string(data), nil
}

// splitOutputLines splits at LF and drops one CR before each LF. A final line
// without LF keeps any CR.
func splitOutputLines(text string) []string {
	lines := strings.Split(text, "\n")
	unterminated := lines[len(lines)-1]
	lines = lines[:len(lines)-1]
	for index := range lines {
		lines[index] = strings.TrimSuffix(lines[index], "\r")
	}
	if unterminated != "" {
		lines = append(lines, unterminated)
	}
	return lines
}

func isBlankDelimiter(delimiter string) bool {
	return strings.Trim(delimiter, delimiterWhitespace) == ""
}

func checkOutputKey(file outputFile, line int, key string) error {
	if len(key) > maxOutputKeyLength || !outputKeyPattern.MatchString(key) {
		return invalidLine(file, line, "key must match %s and be at most %d characters", outputKeyPattern.String(), maxOutputKeyLength)
	}
	for _, prefix := range reservedOutputKeyPrefix {
		if strings.HasPrefix(strings.ToUpper(key), prefix) {
			return invalidLine(file, line, "key %q may not start with %s", key, prefix)
		}
	}
	return nil
}

// publicKeyValues is the public half of a key-value pair, which keys of the
// secret half may not repeat.
type publicKeyValues struct {
	file    outputFile
	entries []keyValue
}

// parseKeyValues reads KEY=VALUE lines and KEY<<DELIMITER blocks; whichever of
// "=" and "<<" comes first on a line decides its form.
func parseKeyValues(file outputFile, lines []string, public *publicKeyValues) ([]keyValue, error) {
	var entries []keyValue
	hasKey := func(entries []keyValue, key string) bool {
		return slices.ContainsFunc(entries, func(entry keyValue) bool { return entry.key == key })
	}
	for index := 0; index < len(lines); {
		line, lineNumber := lines[index], index+1
		index++
		if line == "" {
			continue
		}
		equals, heredoc := strings.Index(line, "="), strings.Index(line, heredocMarker)
		var key, value string
		switch {
		case equals >= 0 && (heredoc < 0 || equals < heredoc):
			key = line[:equals]
			if err := checkOutputKey(file, lineNumber, key); err != nil {
				return nil, err
			}
			value = line[equals+1:]
		case heredoc >= 0:
			key = line[:heredoc]
			if err := checkOutputKey(file, lineNumber, key); err != nil {
				return nil, err
			}
			delimiter := line[heredoc+len(heredocMarker):]
			if isBlankDelimiter(delimiter) {
				return nil, invalidLine(file, lineNumber, "key %q has an empty delimiter", key)
			}
			closing := slices.Index(lines[index:], delimiter)
			if closing < 0 {
				return nil, invalidLine(file, lineNumber, "key %q is missing its closing delimiter line", key)
			}
			value = strings.Join(lines[index:index+closing], "\n")
			index += closing + 1
		default:
			return nil, invalidLine(file, lineNumber, "is not KEY=VALUE or KEY<<DELIMITER")
		}
		if value == "" {
			return nil, invalidLine(file, lineNumber, "key %q has an empty value", key)
		}
		if hasKey(entries, key) {
			return nil, invalidLine(file, lineNumber, "key %q appears twice", key)
		}
		if public != nil && hasKey(public.entries, key) {
			return nil, invalidLine(file, lineNumber, "key %q also appears in %s", key, public.file.envName)
		}
		entries = append(entries, keyValue{key: key, value: value})
	}
	return entries, nil
}

// checkKeyValueLimits applies a pair's size limits. A zero maxValueBytes
// leaves single values unbounded.
func checkKeyValueLimits(public, secret outputFile, publicEntries, secretEntries []keyValue, maxKeys, maxValueBytes, maxTotalBytes int) error {
	totalBytes := 0
	for index, entries := range [][]keyValue{publicEntries, secretEntries} {
		file := []outputFile{public, secret}[index]
		for _, entry := range entries {
			if maxValueBytes > 0 && len(entry.value) > maxValueBytes {
				return invalidOutput("%s: key %q has a value over %d bytes", file.envName, entry.key, maxValueBytes)
			}
			totalBytes += len(entry.key) + len(entry.value)
		}
	}
	if len(publicEntries)+len(secretEntries) > maxKeys {
		return invalidOutput("%s and %s together hold more than %d keys", public.envName, secret.envName, maxKeys)
	}
	if totalBytes > maxTotalBytes {
		return invalidOutput("%s and %s together hold more than %d bytes", public.envName, secret.envName, maxTotalBytes)
	}
	return nil
}

func keyValueMap(entries []keyValue) map[string]string {
	if len(entries) == 0 {
		return nil
	}
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		values[entry.key] = entry.value
	}
	return values
}

// checkWebLaunchEnv applies the web runtime's environment override rules,
// which the backend enforces for web sessions too.
func checkWebLaunchEnv(target outputTarget, public, secret map[string]string) error {
	if !target.webAcceptsLaunchEnv {
		file := launchEnvFile
		if len(public) == 0 {
			file = secretLaunchEnvFile
		}
		return invalidOutput("%s applies only to a web app Revyl runs with a start command, not a deployed URL or static bundle", file.envName)
	}
	if len(public)+len(secret) > maxWebLaunchEnvKeys {
		return invalidOutput("%s and %s together hold more than %d keys, the web limit", launchEnvFile.envName, secretLaunchEnvFile.envName, maxWebLaunchEnvKeys)
	}
	totalBytes := 0
	for index, values := range []map[string]string{public, secret} {
		file := []outputFile{launchEnvFile, secretLaunchEnvFile}[index]
		keys := make([]string, 0, len(values))
		for key := range values {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			value := values[key]
			switch {
			case !webLaunchEnvKeyPattern.MatchString(key):
				return invalidOutput("%s: key %q must match %s on web", file.envName, key, webLaunchEnvKeyPattern.String())
			case slices.Contains(reservedWebLaunchEnvKeys, strings.ToUpper(key)):
				return invalidOutput("%s: key %q is reserved by the web runtime", file.envName, key)
			case len(value) > maxWebLaunchEnvValueBytes:
				return invalidOutput("%s: key %q has a value over %d bytes, the web limit", file.envName, key, maxWebLaunchEnvValueBytes)
			case strings.ContainsFunc(value, unicode.IsControl):
				return invalidOutput("%s: key %q has a control character, which web launch env can't hold", file.envName, key)
			}
			totalBytes += len(key) + len(value)
		}
	}
	if totalBytes > maxWebLaunchEnvTotalBytes {
		return invalidOutput("%s and %s together hold more than %d bytes, the web limit", launchEnvFile.envName, secretLaunchEnvFile.envName, maxWebLaunchEnvTotalBytes)
	}
	return nil
}

// parseArguments reads one token per line, with <<DELIMITER blocks for
// multi-line tokens. Blank lines at the end of the file are ignored.
func parseArguments(lines []string) ([]string, error) {
	file := iosLaunchArgumentsFile
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	var arguments []string
	for index := 0; index < len(lines); {
		line, lineNumber := lines[index], index+1
		index++
		argument := line
		if delimiter, isBlock := strings.CutPrefix(line, heredocMarker); isBlock {
			if isBlankDelimiter(delimiter) {
				return nil, invalidLine(file, lineNumber, "argument has an empty delimiter")
			}
			closing := slices.Index(lines[index:], delimiter)
			if closing < 0 {
				return nil, invalidLine(file, lineNumber, "argument is missing its closing delimiter line")
			}
			argument = strings.Join(lines[index:index+closing], "\n")
			index += closing + 1
		}
		if argument == "" {
			return nil, invalidLine(file, lineNumber, "argument is empty")
		}
		arguments = append(arguments, argument)
	}
	return arguments, nil
}

// parseDeepLink returns the one non-empty line of a deep link file that holds
// text.
func parseDeepLink(lines []string) (string, error) {
	file := deepLinkFile
	var link *string
	for index, line := range lines {
		if line == "" {
			continue
		}
		if link != nil {
			return "", invalidLine(file, index+1, "holds a second non-empty line")
		}
		link = &line
	}
	if link == nil {
		return "", invalidLine(file, 1, "holds no deep link")
	}
	return *link, nil
}

func checkDeepLinkForPlatform(link, platform string) error {
	file := deepLinkFile
	if platform != "web" {
		parts, splits := urlSplit(link)
		if !splits {
			return invalidOutput("%s: a mobile deep link is a URL with a scheme and a valid host", file.envName)
		}
		if parts.scheme == "" {
			return invalidOutput("%s: a mobile deep link is a URL with a scheme", file.envName)
		}
		return nil
	}
	if strings.Contains(link, "${") {
		return invalidOutput("%s: a web deep link cannot contain ${NAME} references", file.envName)
	}
	isOriginPath := strings.HasPrefix(link, "/") && !strings.HasPrefix(link, "//")
	if !isOriginPath && !isHTTPURL(link) {
		return invalidOutput("%s: a web deep link is a path on the origin starting with / or a full http(s) URL", file.envName)
	}
	return nil
}

// splitURL splits a URL the way Python's urllib.parse.urlsplit does, which
// is what the shared rules judge deep links and storage-state origins by.
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

func isHTTPURL(value string) bool {
	parts, splits := urlSplit(value)
	return splits && (parts.scheme == "http" || parts.scheme == "https") && parts.netloc != ""
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

// parseBrowserStorageState validates a Playwright storage state document the
// way BrowserStorageState does in pydantic's lax mode and returns its
// canonical form. Errors name file and never echo a value.
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

// storageStateIssue is the first error pydantic reports for a storage state
// document: a location, or the reason a model validator gave.
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
