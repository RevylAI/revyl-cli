package pinnedskills

import (
	"bufio"
	"embed"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"unicode"
)

// commands/README.md keeps the directory embeddable while it holds no tree, so
// the recorder still builds and can record the first one.
//
//go:embed commands
var recordedCommands embed.FS

// CommandTree is the command tree of one recorded CLI release.
type CommandTree struct {
	releaseTag       string
	globalFlags      flagSet
	feedbackTypeHelp string
	commands         map[string]flagSet
}

// flagSet maps each long flag, such as --chdir, to whether it takes a value.
type flagSet map[string]bool

// parseFlags reads recorded flags, where a value-taking flag ends in "=".
func parseFlags(fields []string) flagSet {
	flags := flagSet{}
	for _, field := range fields {
		name, takesValue := strings.CutSuffix(field, "=")
		flags[name] = takesValue
	}
	return flags
}

// LoadCommandTree returns the recorded command tree of a pinned release tag
// such as v0.1.215.
func LoadCommandTree(releaseTag string) (*CommandTree, error) {
	data, err := recordedCommands.ReadFile("commands/" + releaseTag + ".txt")
	if err != nil {
		return nil, fmt.Errorf("no recorded command tree for %s; record it in revyl-cli/internal/pinnedskills/commands/ as the package documentation describes: %w", releaseTag, err)
	}
	return ParseCommandTree(releaseTag, data)
}

// RecordedReleaseTags returns the release tags whose command trees are
// recorded in commands/.
func RecordedReleaseTags() ([]string, error) {
	entries, err := recordedCommands.ReadDir("commands")
	if err != nil {
		return nil, err
	}
	var tags []string
	for _, entry := range entries {
		if tag, ok := strings.CutSuffix(entry.Name(), ".txt"); ok {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

// ParseCommandTree reads a commands/<release tag>.txt record. It rejects a
// record that is not exactly one global line, one feedback-type line, and at
// least one command line, each in the form CommandTreeText writes.
func ParseCommandTree(releaseTag string, data []byte) (*CommandTree, error) {
	tree := &CommandTree{releaseTag: releaseTag, commands: map[string]flagSet{"": {}}}
	sawGlobal, sawFeedbackType := false, false
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	for number := 1; scanner.Scan(); number++ {
		line := scanner.Text()
		malformed := fmt.Errorf("%s command tree line %d is malformed: %q", releaseTag, number, line)
		switch {
		case (line == "global" || strings.HasPrefix(line, "global ")) && !sawGlobal:
			flags := strings.Fields(strings.TrimPrefix(line, "global"))
			if slices.ContainsFunc(flags, func(field string) bool { return !recordedFlag.MatchString(field) }) {
				return nil, malformed
			}
			tree.globalFlags = parseFlags(flags)
			sawGlobal = true
		case strings.HasPrefix(line, "feedback-type ") && !sawFeedbackType:
			tree.feedbackTypeHelp = strings.TrimPrefix(line, "feedback-type ")
			sawFeedbackType = true
		case strings.HasPrefix(line, "command "):
			var path, flags []string
			for _, field := range strings.Fields(strings.TrimPrefix(line, "command ")) {
				switch {
				case recordedFlag.MatchString(field):
					flags = append(flags, field)
				case commandWord.MatchString(field) && len(flags) == 0:
					path = append(path, field)
				default:
					return nil, malformed
				}
			}
			if len(path) == 0 {
				return nil, malformed
			}
			tree.commands[strings.Join(path, " ")] = parseFlags(flags)
		default:
			return nil, malformed
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !sawGlobal || !sawFeedbackType || len(tree.commands) == 1 {
		return nil, fmt.Errorf("%s command tree is incomplete: it needs a global line, a feedback-type line and at least one command", releaseTag)
	}
	return tree, nil
}

var (
	fencedBlock       = regexp.MustCompile("(?s)```[^\n]*\n(.*?)```")
	inlineCode        = regexp.MustCompile("`((?:[^`\n]|\n[^`\n])+)`")
	commandStart      = regexp.MustCompile(`(?:^|[\s;&|(=])(?:revyl|"\$LAUNCHER"|\$LAUNCHER|"%LAUNCHER%"|%LAUNCHER%)\s+`)
	commandEnd        = regexp.MustCompile(`&&|\|\||[;|)#]`)
	commandWord       = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
	recordedFlag      = regexp.MustCompile(`^--[a-z0-9][a-z0-9-]*=?$`)
	setupCommand      = regexp.MustCompile(`\b(?:revyl|"\$LAUNCHER"|\$LAUNCHER)\s+setup\b`)
	upgradeCommand    = regexp.MustCompile(`(?i)\b(?:revyl|"\$LAUNCHER"|\$LAUNCHER)\s+upgrade\b|\bupgrade (?:it|revyl|the (?:revyl )?cli)\b`)
	frictionFeedback  = regexp.MustCompile(`--type[ =]friction\b`)
	positionalChoices = []string{"ios", "android", "web"}
)

// Problems returns each statement in plugin Markdown that the pinned release
// cannot follow: revyl setup, --intent, an instruction to upgrade the CLI, a
// feedback type it lacks, and any revyl command or flag missing from its
// command tree.
func (tree *CommandTree) Problems(markdown string) []string {
	var problems []string
	if setupCommand.MatchString(markdown) {
		problems = append(problems, "runs revyl setup; plugins must not depend on it")
	}
	if strings.Contains(markdown, "--intent") {
		problems = append(problems, "mentions --intent; plugins must not depend on it")
	}
	if match := upgradeCommand.FindString(markdown); match != "" {
		problems = append(problems, fmt.Sprintf("tells the agent to upgrade the pinned CLI (%q); the plugin moves its own pin", match))
	}
	if frictionFeedback.MatchString(markdown) && !strings.Contains(tree.feedbackTypeHelp, "friction") {
		problems = append(problems, fmt.Sprintf("uses feedback --type friction, which %s lacks (%s)", tree.releaseTag, tree.feedbackTypeHelp))
	}
	for _, snippet := range codeSnippets(markdown) {
		for _, location := range commandStart.FindAllStringIndex(snippet, -1) {
			problems = append(problems, tree.commandProblems(snippet[location[1]:])...)
		}
	}
	return problems
}

func codeSnippets(markdown string) []string {
	var snippets []string
	for _, match := range fencedBlock.FindAllStringSubmatch(markdown, -1) {
		snippets = append(snippets, strings.Split(strings.ReplaceAll(match[1], "\\\n", " "), "\n")...)
	}
	for _, match := range inlineCode.FindAllStringSubmatch(fencedBlock.ReplaceAllString(markdown, ""), -1) {
		snippets = append(snippets, strings.Join(strings.Fields(match[1]), " "))
	}
	return snippets
}

func (tree *CommandTree) commandProblems(invocation string) []string {
	if location := commandEnd.FindStringIndex(invocation); location != nil {
		invocation = invocation[:location[0]]
	}
	var path, usedFlags []string
	flags := tree.flagsFor(path)
	pathDone, skipValue, afterUntypedFlag := false, false, false
	for _, word := range shellWords(invocation) {
		if skipValue {
			skipValue = false
			continue
		}
		if word.text == "--" {
			break
		}
		if word.isFlag() {
			name, _, inlineValue := strings.Cut(word.text, "=")
			if strings.HasPrefix(name, "--") {
				// Plugin prose can end a sentence right after a flag.
				name = strings.TrimRight(name, ".,:;!?")
				usedFlags = append(usedFlags, name)
			}
			takesValue, known := flags[name]
			skipValue = known && takesValue && !inlineValue
			afterUntypedFlag = !known && !inlineValue
			continue
		}
		// A word after a flag this release does not record, such as a short
		// flag, may be that flag's value.
		untypedValue := afterUntypedFlag
		afterUntypedFlag = false
		if pathDone {
			continue
		}
		if !commandWord.MatchString(word.text) {
			pathDone = !untypedValue
			continue
		}
		candidate := strings.Join(append(slices.Clone(path), word.text), " ")
		if _, ok := tree.commands[candidate]; ok {
			path = append(path, word.text)
			flags = tree.flagsFor(path)
			continue
		}
		if untypedValue {
			continue
		}
		if tree.hasSubcommands(strings.Join(path, " ")) && !slices.Contains(positionalChoices, word.text) {
			return []string{fmt.Sprintf("%s has no command %q", tree.releaseTag, "revyl "+candidate)}
		}
		pathDone = true
	}
	if len(path) == 0 {
		return nil
	}
	var problems []string
	for _, name := range usedFlags {
		if _, ok := flags[name]; !ok {
			problems = append(problems, fmt.Sprintf("%s has no %s flag on %q", tree.releaseTag, name, "revyl "+strings.Join(path, " ")))
		}
	}
	return problems
}

// shellWord is one word of a shell command. plainStart reports that its first
// character was neither quoted nor escaped, so only such a word can be a flag.
type shellWord struct {
	text       string
	plainStart bool
}

func (word shellWord) isFlag() bool {
	return word.plainStart && len(word.text) > 1 && strings.HasPrefix(word.text, "-")
}

// shellWords splits a command into words the way a POSIX shell does, honoring
// single quotes, double quotes and backslash escapes.
func shellWords(command string) []shellWord {
	var words []shellWord
	var word strings.Builder
	inWord, plainStart, escaped := false, false, false
	var quote rune
	startWord := func(plain bool) {
		if !inWord {
			inWord, plainStart = true, plain
		}
	}
	for _, character := range command {
		switch {
		case escaped:
			word.WriteRune(character)
			escaped = false
		case quote == '\'':
			if character == '\'' {
				quote = 0
			} else {
				word.WriteRune(character)
			}
		case character == '\\':
			startWord(false)
			escaped = true
		case quote == '"':
			if character == '"' {
				quote = 0
			} else {
				word.WriteRune(character)
			}
		case character == '\'' || character == '"':
			startWord(false)
			quote = character
		case unicode.IsSpace(character):
			if inWord {
				words = append(words, shellWord{text: word.String(), plainStart: plainStart})
				word.Reset()
				inWord = false
			}
		default:
			startWord(true)
			word.WriteRune(character)
		}
	}
	if inWord {
		words = append(words, shellWord{text: word.String(), plainStart: plainStart})
	}
	return words
}

// flagsFor returns the flags a command accepts: the global flags, --help,
// and the flags of the command and each of its parents.
func (tree *CommandTree) flagsFor(path []string) flagSet {
	// Cobra gives every command --help, and the schema does not record it.
	flags := flagSet{"--help": false}
	for name, takesValue := range tree.globalFlags {
		flags[name] = takesValue
	}
	for depth := 1; depth <= len(path); depth++ {
		for name, takesValue := range tree.commands[strings.Join(path[:depth], " ")] {
			flags[name] = takesValue
		}
	}
	return flags
}

func (tree *CommandTree) hasSubcommands(path string) bool {
	prefix := path + " "
	if path == "" {
		prefix = ""
	}
	for candidate := range tree.commands {
		if candidate != path && strings.HasPrefix(candidate, prefix) {
			return true
		}
	}
	return false
}
