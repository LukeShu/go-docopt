/*
Based of off docopt.py: https://github.com/docopt/docopt

Licensed under terms of MIT license (see LICENSE-MIT)
Copyright (c) 2013 Keith Batten, kbatten@gmail.com
*/

package docopt

import (
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strings"
	"unicode"
)

// parse and return a map of args, output and all errors
func Parse(doc string, argv []string, help bool, version string, optionsFirst bool) (args map[string]interface{}, output string, err error) {
	if argv == nil && len(os.Args) > 1 {
		argv = os.Args[1:]
	}

	usageSections := parseSection("usage:", doc)

	if len(usageSections) == 0 {
		err = newLanguageError("\"usage:\" (case-insensitive) not found.")
		return
	}
	if len(usageSections) > 1 {
		err = newLanguageError("More than one \"usage:\" (case-insensitive).")
		return
	}
	usage := usageSections[0]

	options := parseDefaults(doc)
	pat, err := parsePattern(formalUsage(usage), &options)
	if err != nil {
		output = handleError(err, usage)
		return
	}

	patternArgv, err := parseArgv(newTokenList(argv, ERROR_USER), &options, optionsFirst)
	if err != nil {
		output = handleError(err, usage)
		return
	}
	patFlat, err := pat.flat(PATTERN_OPTION)
	if err != nil {
		output = handleError(err, usage)
		return
	}
	patternOptions := patFlat.unique()

	patFlat, err = pat.flat(PATTERN_OPTIONSSHORTCUT)
	if err != nil {
		output = handleError(err, usage)
		return
	}
	for _, optionsShortcut := range patFlat {
		docOptions := parseDefaults(doc)
		optionsShortcut.children = docOptions.unique().diff(patternOptions)
	}

	if output = extras(help, version, patternArgv, doc); len(output) > 0 {
		return
	}

	err = pat.fix()
	if err != nil {
		output = handleError(err, usage)
		return
	}
	matched, left, collected := pat.match(&patternArgv, nil)
	if matched && len(*left) == 0 {
		patFlat, err = pat.flat(PATTERN_DEFAULT)
		if err != nil {
			output = handleError(err, usage)
			return
		}
		args = append(patFlat, *collected...).dictionary()
		return
	}

	err = newUserError("")
	output = handleError(err, usage)
	return
}

// parse just doc and return a map of args
// handle all printing and non-fatal errors
// panic on fatal errors
// exit on user error or help
func ParseEasy(doc string) map[string]interface{} {
	return ParseLoud(doc, nil, true, "", false)
}

// parse and return a map of args and fatal errors
// handle printing of help
// exit on user error or help
func ParseQuiet(doc string, argv []string, help bool, version string, optionsFirst bool) (map[string]interface{}, error) {
	args, output, err := Parse(doc, argv, help, version, optionsFirst)
	if _, ok := err.(*UserError); ok {
		fmt.Println(output)
		os.Exit(1)
	} else if len(output) > 0 && err == nil {
		fmt.Println(output)
		os.Exit(0)
	}
	return args, err
}

// parse and return a map of args
// handle all printing and non-fatal errors
// panic on fatal errors
// exit on user error or help
func ParseLoud(doc string, argv []string, help bool, version string, optionsFirst bool) map[string]interface{} {
	args, err := ParseQuiet(doc, argv, help, version, optionsFirst)
	if _, ok := err.(*LanguageError); ok {
		panic(fmt.Sprintf("(language) %s", err))
	} else if err != nil {
		panic(fmt.Sprintf("(internal) %s", err))
	}
	return args
}

func handleError(err error, usage string) string {
	if _, ok := err.(*UserError); ok {
		return strings.TrimSpace(fmt.Sprintf("%s\n%s", err, usage))
	}
	return ""
}

func parseSection(name, source string) []string {
	p := regexp.MustCompile(`(?im)^([^\n]*` + name + `[^\n]*\n?(?:[ \t].*?(?:\n|$))*)`)
	s := p.FindAllString(source, -1)
	if s == nil {
		s = []string{}
	}
	for i, v := range s {
		s[i] = strings.TrimSpace(v)
	}
	return s
}

func parseDefaults(doc string) patternList {
	defaults := patternList{}
	p := regexp.MustCompile(`\n[ \t]*(-\S+?)`)
	for _, s := range parseSection("options:", doc) {
		// FIXME corner case "bla: options: --foo"
		_, _, s = stringPartition(s, ":") // get rid of "options:"
		split := p.Split("\n"+s, -1)[1:]
		match := p.FindAllStringSubmatch("\n"+s, -1)
		for i := range split {
			optionDescription := match[i][1] + split[i]
			if strings.HasPrefix(optionDescription, "-") {
				defaults = append(defaults, parseOption(optionDescription))
			}
		}
	}
	return defaults
}

func parsePattern(source string, options *patternList) (*pattern, error) {
	tokens := tokenListFromPattern(source)
	result, err := parseExpr(tokens, options)
	if err != nil {
		return nil, err
	}
	if tokens.current() != nil {
		return nil, tokens.errorFunc("unexpected ending: %s" + strings.Join(tokens.tokens, " "))
	}
	return newRequired(result...), nil
}

func parseArgv(tokens *tokenList, options *patternList, optionsFirst bool) (patternList, error) {
	/*
		Parse command-line argument vector.

		If options_first:
			argv ::= [ long | shorts ]* [ argument ]* [ '--' [ argument ]* ] ;
		else:
			argv ::= [ long | shorts | argument ]* [ '--' [ argument ]* ] ;
	*/
	parsed := patternList{}
	for tokens.current() != nil {
		if tokens.current().eq("--") {
			for _, v := range tokens.tokens {
				parsed = append(parsed, newArgument("", v))
			}
			return parsed, nil
		} else if tokens.current().hasPrefix("--") {
			pl, err := parseLong(tokens, options)
			if err != nil {
				return nil, err
			}
			parsed = append(parsed, pl...)
		} else if tokens.current().hasPrefix("-") && !tokens.current().eq("-") {
			ps, err := parseShorts(tokens, options)
			if err != nil {
				return nil, err
			}
			parsed = append(parsed, ps...)
		} else if optionsFirst {
			for _, v := range tokens.tokens {
				parsed = append(parsed, newArgument("", v))
			}
			return parsed, nil
		} else {
			parsed = append(parsed, newArgument("", tokens.move().String()))
		}
	}
	return parsed, nil
}

func parseOption(optionDescription string) *pattern {
	optionDescription = strings.TrimSpace(optionDescription)
	options, _, description := stringPartition(optionDescription, "  ")
	options = strings.Replace(options, ",", " ", -1)
	options = strings.Replace(options, "=", " ", -1)

	short := ""
	long := ""
	argcount := 0
	var value interface{}
	value = false

	reDefault := regexp.MustCompile(`(?i)\[default: (.*)\]`)
	for _, s := range strings.Fields(options) {
		if strings.HasPrefix(s, "--") {
			long = s
		} else if strings.HasPrefix(s, "-") {
			short = s
		} else {
			argcount = 1
		}
		if argcount > 0 {
			matched := reDefault.FindAllStringSubmatch(description, -1)
			if len(matched) > 0 {
				value = matched[0][1]
			} else {
				value = nil
			}
		}
	}
	return newOption(short, long, argcount, value)
}

func parseExpr(tokens *tokenList, options *patternList) (patternList, error) {
	// expr ::= seq ( '|' seq )* ;
	seq, err := parseSeq(tokens, options)
	if err != nil {
		return nil, err
	}
	if !tokens.current().eq("|") {
		return seq, nil
	}
	var result patternList
	if len(seq) > 1 {
		result = patternList{newRequired(seq...)}
	} else {
		result = seq
	}
	for tokens.current().eq("|") {
		tokens.move()
		seq, err = parseSeq(tokens, options)
		if err != nil {
			return nil, err
		}
		if len(seq) > 1 {
			result = append(result, newRequired(seq...))
		} else {
			result = append(result, seq...)
		}
	}
	if len(result) > 1 {
		return patternList{newEither(result...)}, nil
	}
	return result, nil
}

func parseSeq(tokens *tokenList, options *patternList) (patternList, error) {
	// seq ::= ( atom [ '...' ] )* ;
	result := patternList{}
	for !tokens.current().match(true, "]", ")", "|") {
		atom, err := parseAtom(tokens, options)
		if err != nil {
			return nil, err
		}
		if tokens.current().eq("...") {
			atom = patternList{newOneOrMore(atom...)}
			tokens.move()
		}
		result = append(result, atom...)
	}
	return result, nil
}

func parseAtom(tokens *tokenList, options *patternList) (patternList, error) {
	// atom ::= '(' expr ')' | '[' expr ']' | 'options' | long | shorts | argument | command ;
	tok := tokens.current()
	result := patternList{}
	if tokens.current().match(false, "(", "[") {
		tokens.move()
		var matching string
		pl, err := parseExpr(tokens, options)
		if err != nil {
			return nil, err
		}
		if tok.eq("(") {
			matching = ")"
			result = patternList{newRequired(pl...)}
		} else if tok.eq("[") {
			matching = "]"
			result = patternList{newOptional(pl...)}
		}
		moved := tokens.move()
		if !moved.eq(matching) {
			return nil, tokens.errorFunc("unmatched '%s', expected: '%s' got: '%s'", tok, matching, moved)
		}
		return result, nil
	} else if tok.eq("options") {
		tokens.move()
		return patternList{newOptionsShortcut()}, nil
	} else if tok.hasPrefix("--") && !tok.eq("--") {
		return parseLong(tokens, options)
	} else if tok.hasPrefix("-") && !tok.eq("-") && !tok.eq("--") {
		return parseShorts(tokens, options)
	} else if tok.hasPrefix("<") && tok.hasSuffix(">") || tok.isUpper() {
		return patternList{newArgument(tokens.move().String(), nil)}, nil
	}
	return patternList{newCommand(tokens.move().String(), false)}, nil
}

func parseLong(tokens *tokenList, options *patternList) (patternList, error) {
	// long ::= '--' chars [ ( ' ' | '=' ) chars ] ;
	long, eq, v := stringPartition(tokens.move().String(), "=")
	var value interface{}
	var opt *pattern
	if eq == "" && v == "" {
		value = nil
	} else {
		value = v
	}

	if !strings.HasPrefix(long, "--") {
		return nil, newError("long option '%s' doesn't start with --", long)
	}
	similar := patternList{}
	for _, o := range *options {
		if o.long == long {
			similar = append(similar, o)
		}
	}
	if tokens.err == ERROR_USER && len(similar) == 0 { // if no exact match
		similar = patternList{}
		for _, o := range *options {
			if strings.HasPrefix(o.long, long) {
				similar = append(similar, o)
			}
		}
	}
	if len(similar) > 1 { // might be simply specified ambiguously 2+ times?
		similarLong := make([]string, len(similar))
		for i, s := range similar {
			similarLong[i] = s.long
		}
		return nil, tokens.errorFunc("%s is not a unique prefix: %s?", long, strings.Join(similarLong, ", "))
	} else if len(similar) < 1 {
		argcount := 0
		if eq == "=" {
			argcount = 1
		}
		opt = newOption("", long, argcount, false)
		*options = append(*options, opt)
		if tokens.err == ERROR_USER {
			var val interface{}
			if argcount > 0 {
				val = value
			} else {
				val = true
			}
			opt = newOption("", long, argcount, val)
		}
	} else {
		opt = newOption(similar[0].short, similar[0].long, similar[0].argcount, similar[0].value)
		if opt.argcount == 0 {
			if value != nil {
				return nil, tokens.errorFunc("%s must not have an argument", opt.long)
			}
		} else {
			if value == nil {
				if tokens.current().match(true, "--") {
					return nil, tokens.errorFunc("%s requires argument", opt.long)
				}
				moved := tokens.move()
				if moved != nil {
					value = moved.String() // only set as string if not nil
				}
			}
		}
		if tokens.err == ERROR_USER {
			if value != nil {
				opt.value = value
			} else {
				opt.value = true
			}
		}
	}

	return patternList{opt}, nil
}

func parseShorts(tokens *tokenList, options *patternList) (patternList, error) {
	// shorts ::= '-' ( chars )* [ [ ' ' ] chars ] ;
	tok := tokens.move()
	if !tok.hasPrefix("-") || tok.hasPrefix("--") {
		return nil, newError("short option '%s' doesn't start with -", tok)
	}
	left := strings.TrimLeft(tok.String(), "-")
	parsed := patternList{}
	for left != "" {
		var opt *pattern
		short := "-" + left[0:1]
		left = left[1:]
		similar := patternList{}
		for _, o := range *options {
			if o.short == short {
				similar = append(similar, o)
			}
		}
		if len(similar) > 1 {
			return nil, tokens.errorFunc("%s is specified ambiguously %d times", short, len(similar))
		} else if len(similar) < 1 {
			opt = newOption(short, "", 0, false)
			*options = append(*options, opt)
			if tokens.err == ERROR_USER {
				opt = newOption(short, "", 0, true)
			}
		} else { // why copying is necessary here?
			opt = newOption(short, similar[0].long, similar[0].argcount, similar[0].value)
			var value interface{}
			if opt.argcount > 0 {
				if left == "" {
					if tokens.current().match(true, "--") {
						return nil, tokens.errorFunc("%s requires argument", short)
					}
					value = tokens.move().String()
				} else {
					value = left
					left = ""
				}
			}
			if tokens.err == ERROR_USER {
				if value != nil {
					opt.value = value
				} else {
					opt.value = true
				}
			}
		}
		parsed = append(parsed, opt)
	}
	return parsed, nil
}

func newTokenList(source []string, err errorType) *tokenList {
	errorFunc := newError
	if err == ERROR_USER {
		errorFunc = newUserError
	} else if err == ERROR_LANGUAGE {
		errorFunc = newLanguageError
	}
	return &tokenList{source, errorFunc, err}
}

func tokenListFromString(source string) *tokenList {
	return newTokenList(strings.Fields(source), ERROR_USER)
}

func tokenListFromPattern(source string) *tokenList {
	p := regexp.MustCompile(`([\[\]\(\)\|]|\.\.\.)`)
	source = p.ReplaceAllString(source, ` $1 `)
	p = regexp.MustCompile(`\s+|(\S*<.*?>)`)
	split := p.Split(source, -1)
	match := p.FindAllStringSubmatch(source, -1)
	var result []string
	l := len(split)
	for i := 0; i < l; i++ {
		if len(split[i]) > 0 {
			result = append(result, split[i])
		}
		if i < l-1 && len(match[i][1]) > 0 {
			result = append(result, match[i][1])
		}
	}
	return newTokenList(result, ERROR_LANGUAGE)
}

func formalUsage(section string) string {
	_, _, section = stringPartition(section, ":") // drop "usage:"
	pu := strings.Fields(section)

	result := "( "
	for _, s := range pu[1:] {
		if s == pu[0] {
			result += ") | ( "
		} else {
			result += s + " "
		}
	}
	result += ")"

	return result
}

func extras(help bool, version string, options patternList, doc string) string {
	if help {
		for _, o := range options {
			if (o.name == "-h" || o.name == "--help") && o.value == true {
				return strings.Trim(doc, "\n")
			}
		}
	}
	if version != "" {
		for _, o := range options {
			if (o.name == "--version") && o.value == true {
				return version
			}
		}
	}
	return ""
}

type errorType int

const (
	ERROR_USER errorType = iota
	ERROR_LANGUAGE
)

func (self errorType) String() string {
	switch self {
	case ERROR_USER:
		return "userError"
	case ERROR_LANGUAGE:
		return "languageError"
	}
	return ""
}

type UserError struct {
	msg   string
	Usage string
}

func (e UserError) Error() string {
	return e.msg
}
func newUserError(msg string, f ...interface{}) error {
	return &UserError{fmt.Sprintf(msg, f...), ""}
}

type LanguageError struct {
	msg string
}

func (e LanguageError) Error() string {
	return e.msg
}
func newLanguageError(msg string, f ...interface{}) error {
	return &LanguageError{fmt.Sprintf(msg, f...)}
}

var newError = fmt.Errorf

type tokenList struct {
	tokens    []string
	errorFunc func(string, ...interface{}) error
	err       errorType
}
type token string

func (self *token) eq(s string) bool {
	if self == nil {
		return false
	}
	return string(*self) == s
}
func (self *token) match(matchNil bool, tokenStrings ...string) bool {
	if self == nil && matchNil {
		return true
	} else if self == nil && !matchNil {
		return false
	}

	for _, t := range tokenStrings {
		if t == string(*self) {
			return true
		}
	}
	return false
}
func (self *token) hasPrefix(prefix string) bool {
	if self == nil {
		return false
	}
	return strings.HasPrefix(string(*self), prefix)
}
func (self *token) hasSuffix(suffix string) bool {
	if self == nil {
		return false
	}
	return strings.HasSuffix(string(*self), suffix)
}
func (self *token) isUpper() bool {
	if self == nil {
		return false
	}
	return isStringUppercase(string(*self))
}
func (self *token) String() string {
	if self == nil {
		return ""
	}
	return string(*self)
}

func (self *tokenList) current() *token {
	if len(self.tokens) > 0 {
		return (*token)(&(self.tokens[0]))
	}
	return nil
}

func (self *tokenList) length() int {
	return len(self.tokens)
}

func (self *tokenList) move() *token {
	if len(self.tokens) > 0 {
		t := self.tokens[0]
		self.tokens = self.tokens[1:]
		return (*token)(&t)
	}
	return nil
}

type patternType uint

const (
	// leaf
	PATTERN_ARGUMENT patternType = 1 << iota
	PATTERN_COMMAND
	PATTERN_OPTION

	// branch
	PATTERN_REQUIRED
	PATTERN_OPTIONAL
	PATTERN_OPTIONSSHORTCUT // Marker/placeholder for [options] shortcut.
	PATTERN_ONEORMORE
	PATTERN_EITHER

	PATTERN_LEAF = PATTERN_ARGUMENT +
		PATTERN_COMMAND +
		PATTERN_OPTION
	PATTERN_BRANCH = PATTERN_REQUIRED +
		PATTERN_OPTIONAL +
		PATTERN_OPTIONSSHORTCUT +
		PATTERN_ONEORMORE +
		PATTERN_EITHER
	PATTERN_ALL     = PATTERN_LEAF + PATTERN_BRANCH
	PATTERN_DEFAULT = 0
)

func (self patternType) String() string {
	switch self {
	case PATTERN_ARGUMENT:
		return "argument"
	case PATTERN_COMMAND:
		return "command"
	case PATTERN_OPTION:
		return "option"
	case PATTERN_REQUIRED:
		return "required"
	case PATTERN_OPTIONAL:
		return "optional"
	case PATTERN_OPTIONSSHORTCUT:
		return "optionsshortcut"
	case PATTERN_ONEORMORE:
		return "oneormore"
	case PATTERN_EITHER:
		return "either"
	case PATTERN_LEAF:
		return "leaf"
	case PATTERN_BRANCH:
		return "branch"
	case PATTERN_ALL:
		return "all"
	case PATTERN_DEFAULT:
		return "default"
	}
	return ""
}

type pattern struct {
	t patternType

	children patternList

	name  string
	value interface{}

	short    string
	long     string
	argcount int
}

type patternList []*pattern

func newBranchPattern(t patternType, pl ...*pattern) *pattern {
	var p pattern
	p.t = t
	p.children = make(patternList, len(pl))
	copy(p.children, pl)
	return &p
}

func newRequired(pl ...*pattern) *pattern {
	return newBranchPattern(PATTERN_REQUIRED, pl...)
}

func newEither(pl ...*pattern) *pattern {
	return newBranchPattern(PATTERN_EITHER, pl...)
}

func newOneOrMore(pl ...*pattern) *pattern {
	return newBranchPattern(PATTERN_ONEORMORE, pl...)
}

func newOptional(pl ...*pattern) *pattern {
	return newBranchPattern(PATTERN_OPTIONAL, pl...)
}

func newOptionsShortcut() *pattern {
	var p pattern
	p.t = PATTERN_OPTIONSSHORTCUT
	return &p
}

func newLeafPattern(t patternType, name string, value interface{}) *pattern {
	// default: value=nil
	var p pattern
	p.t = t
	p.name = name
	p.value = value
	return &p
}

func newArgument(name string, value interface{}) *pattern {
	// default: value=nil
	return newLeafPattern(PATTERN_ARGUMENT, name, value)
}

func newCommand(name string, value interface{}) *pattern {
	// default: value=false
	var p pattern
	p.t = PATTERN_COMMAND
	p.name = name
	p.value = value
	return &p
}

func newOption(short, long string, argcount int, value interface{}) *pattern {
	// default: "", "", 0, false
	var p pattern
	p.t = PATTERN_OPTION
	p.short = short
	p.long = long
	if long != "" {
		p.name = long
	} else {
		p.name = short
	}
	p.argcount = argcount
	if value == false && argcount > 0 {
		p.value = nil
	} else {
		p.value = value
	}
	return &p
}

func (self *pattern) flat(types patternType) (patternList, error) {
	if self.t&PATTERN_LEAF != 0 {
		if types == PATTERN_DEFAULT {
			types = PATTERN_ALL
		}
		if self.t&types != 0 {
			return patternList{self}, nil
		}
		return patternList{}, nil
	}

	if self.t&PATTERN_BRANCH != 0 {
		if self.t&types != 0 {
			return patternList{self}, nil
		}
		result := patternList{}
		for _, child := range self.children {
			childFlat, err := child.flat(types)
			if err != nil {
				return nil, err
			}
			result = append(result, childFlat...)
		}
		return result, nil
	}
	return nil, newError("unknown pattern type: %d, %d", self.t, types)
}

func (self *pattern) fix() error {
	err := self.fixIdentities(nil)
	if err != nil {
		return err
	}
	self.fixRepeatingArguments()
	return nil
}

func (self *pattern) fixIdentities(uniq patternList) error {
	// Make pattern-tree tips point to same object if they are equal.
	if self.t&PATTERN_BRANCH == 0 {
		return nil
	}
	if uniq == nil {
		selfFlat, err := self.flat(PATTERN_DEFAULT)
		if err != nil {
			return err
		}
		uniq = selfFlat.unique()
	}
	for i, child := range self.children {
		if child.t&PATTERN_BRANCH == 0 {
			ind, err := uniq.index(child)
			if err != nil {
				return err
			}
			self.children[i] = uniq[ind]
		} else {
			err := child.fixIdentities(uniq)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func (self *pattern) fixRepeatingArguments() {
	// Fix elements that should accumulate/increment values.
	var either []patternList

	for _, child := range self.transform().children {
		either = append(either, child.children)
	}
	for _, cas := range either {
		casMultiple := patternList{}
		for _, e := range cas {
			if cas.count(e) > 1 {
				casMultiple = append(casMultiple, e)
			}
		}
		for _, e := range casMultiple {
			if e.t == PATTERN_ARGUMENT || e.t == PATTERN_OPTION && e.argcount > 0 {
				switch e.value.(type) {
				case string:
					e.value = strings.Fields(e.value.(string))
				case []string:
				default:
					e.value = []string{}
				}
			}
			if e.t == PATTERN_COMMAND || e.t == PATTERN_OPTION && e.argcount == 0 {
				e.value = 0
			}
		}
	}
}

func (self *pattern) match(left *patternList, collected *patternList) (bool, *patternList, *patternList) {
	if collected == nil {
		collected = &patternList{}
	}
	if self.t&PATTERN_REQUIRED != 0 {
		l := left
		c := collected
		for _, p := range self.children {
			var matched bool
			matched, l, c = p.match(l, c)
			if !matched {
				return false, left, collected
			}
		}
		return true, l, c
	} else if self.t&PATTERN_OPTIONAL != 0 || self.t&PATTERN_OPTIONSSHORTCUT != 0 {
		for _, p := range self.children {
			_, left, collected = p.match(left, collected)
		}
		return true, left, collected
	} else if self.t&PATTERN_ONEORMORE != 0 {
		if len(self.children) != 1 {
			panic("OneOrMore.match(): assert len(self.children) == 1")
		}
		l := left
		c := collected
		var l_ *patternList
		matched := true
		times := 0
		for matched {
			// could it be that something didn't match but changed l or c?
			matched, l, c = self.children[0].match(l, c)
			if matched {
				times += 1
			}
			if l_ == l {
				break
			}
			l_ = l
		}
		if times >= 1 {
			return true, l, c
		}
		return false, left, collected
	} else if self.t&PATTERN_EITHER != 0 {
		type outcomeStruct struct {
			matched   bool
			left      *patternList
			collected *patternList
			length    int
		}
		outcomes := []outcomeStruct{}
		for _, p := range self.children {
			matched, l, c := p.match(left, collected)
			outcome := outcomeStruct{matched, l, c, len(*l)}
			if matched {
				outcomes = append(outcomes, outcome)
			}
		}
		if len(outcomes) > 0 {
			minLen := outcomes[0].length
			minIndex := 0
			for i, v := range outcomes {
				if v.length < minLen {
					minIndex = i
				}
			}
			return outcomes[minIndex].matched, outcomes[minIndex].left, outcomes[minIndex].collected
		}
		return false, left, collected
	} else if self.t&PATTERN_LEAF != 0 {
		pos, match := self.singleMatch(left)
		var increment interface{}
		if match == nil {
			return false, left, collected
		}
		left_ := make(patternList, len((*left)[:pos]), len((*left)[:pos])+len((*left)[pos+1:]))
		copy(left_, (*left)[:pos])
		left_ = append(left_, (*left)[pos+1:]...)
		sameName := patternList{}
		for _, a := range *collected {
			if a.name == self.name {
				sameName = append(sameName, a)
			}
		}

		switch self.value.(type) {
		case int, []string:
			switch self.value.(type) {
			case int:
				increment = 1
			case []string:
				switch match.value.(type) {
				case string:
					increment = []string{match.value.(string)}
				default:
					increment = match.value
				}
			}
			if len(sameName) == 0 {
				match.value = increment
				collectedMatch := make(patternList, len(*collected), len(*collected)+1)
				copy(collectedMatch, *collected)
				collectedMatch = append(collectedMatch, match)
				return true, &left_, &collectedMatch
			}
			switch sameName[0].value.(type) {
			case int:
				sameName[0].value = sameName[0].value.(int) + increment.(int)
			case []string:
				sameName[0].value = append(sameName[0].value.([]string), increment.([]string)...)
			}
			return true, &left_, collected
		}
		collectedMatch := make(patternList, len(*collected), len(*collected)+1)
		copy(collectedMatch, *collected)
		collectedMatch = append(collectedMatch, match)
		return true, &left_, &collectedMatch
	}
	panic("unmatched type")
	return false, &patternList{}, &patternList{}
}

func (self *pattern) singleMatch(left *patternList) (int, *pattern) {
	if self.t&PATTERN_ARGUMENT != 0 {
		for n, p := range *left {
			if p.t&PATTERN_ARGUMENT != 0 {
				return n, newArgument(self.name, p.value)
			}
		}
		return -1, nil
	} else if self.t&PATTERN_COMMAND != 0 {
		for n, p := range *left {
			if p.t&PATTERN_ARGUMENT != 0 {
				if p.value == self.name {
					return n, newCommand(self.name, true)
				} else {
					break
				}
			}
		}
		return -1, nil
	} else if self.t&PATTERN_OPTION != 0 {
		for n, p := range *left {
			if self.name == p.name {
				return n, p
			}
		}
		return -1, nil
	}
	panic("unmatched type")
	return -1, nil
}

func (self *pattern) String() string {
	if self.t&PATTERN_OPTION != 0 {
		return fmt.Sprintf("%s(%s, %s, %d, %+v)", self.t, self.short, self.long, self.argcount, self.value)
	} else if self.t&PATTERN_LEAF != 0 {
		return fmt.Sprintf("%s(%s, %+v)", self.t, self.name, self.value)
	} else if self.t&PATTERN_BRANCH != 0 {
		result := ""
		for i, child := range self.children {
			if i > 0 {
				result += ", "
			}
			result += child.String()
		}
		return fmt.Sprintf("%s(%s)", self.t, result)
	}
	panic("unmatched type")
	return ""
}

func (self *pattern) transform() *pattern {
	/*
		Expand pattern into an (almost) equivalent one, but with single Either.

		Example: ((-a | -b) (-c | -d)) => (-a -c | -a -d | -b -c | -b -d)
		Quirks: [-a] => (-a), (-a...) => (-a -a)
	*/
	result := []patternList{}
	groups := []patternList{patternList{self}}
	parents := PATTERN_REQUIRED +
		PATTERN_OPTIONAL +
		PATTERN_OPTIONSSHORTCUT +
		PATTERN_EITHER +
		PATTERN_ONEORMORE
	for len(groups) > 0 {
		children := groups[0]
		groups = groups[1:]
		var child *pattern
		for _, c := range children {
			if c.t&parents != 0 {
				child = c
				break
			}
		}
		if child != nil {
			children.remove(child)
			if child.t&PATTERN_EITHER != 0 {
				for _, c := range child.children {
					r := patternList{}
					r = append(r, c)
					r = append(r, children...)
					groups = append(groups, r)
				}
			} else if child.t&PATTERN_ONEORMORE != 0 {
				r := patternList{}
				r = append(r, child.children.double()...)
				r = append(r, children...)
				groups = append(groups, r)
			} else {
				r := patternList{}
				r = append(r, child.children...)
				r = append(r, children...)
				groups = append(groups, r)
			}
		} else {
			result = append(result, children)
		}
	}
	either := patternList{}
	for _, e := range result {
		either = append(either, newRequired(e...))
	}
	return newEither(either...)
}

func (self *pattern) eq(other *pattern) bool {
	return reflect.DeepEqual(self, other)
}

func (pl patternList) unique() patternList {
	table := make(map[string]bool)
	result := patternList{}
	for _, v := range pl {
		if !table[v.String()] {
			table[v.String()] = true
			result = append(result, v)
		}
	}
	return result
}

func (pl patternList) index(p *pattern) (int, error) {
	for i, c := range pl {
		if c.eq(p) {
			return i, nil
		}
	}
	return -1, newError("%s not in list", p)
}

func (pl patternList) count(p *pattern) int {
	count := 0
	for _, c := range pl {
		if c.eq(p) {
			count++
		}
	}
	return count
}

func (pl patternList) diff(l patternList) patternList {
	l_ := make(patternList, len(l))
	copy(l_, l)
	result := make(patternList, 0, len(pl))
	for _, v := range pl {
		if v != nil {
			match := false
			for i, w := range l_ {
				if w.eq(v) {
					match = true
					l_[i] = nil
					break
				}
			}
			if match == false {
				result = append(result, v)
			}
		}
	}
	return result
}

func (pl patternList) double() patternList {
	l := len(pl)
	result := make(patternList, l*2)
	copy(result, pl)
	copy(result[l:2*l], pl)
	return result
}

func (self *patternList) remove(p *pattern) {
	(*self) = self.diff(patternList{p})
}

func (pl patternList) dictionary() map[string]interface{} {
	dict := make(map[string]interface{})
	for _, a := range pl {
		dict[a.name] = a.value
	}
	return dict
}

func stringPartition(s, sep string) (string, string, string) {
	sepPos := strings.Index(s, sep)
	if sepPos == -1 { // no seperator found
		return s, "", ""
	}
	split := strings.SplitN(s, sep, 2)
	return split[0], sep, split[1]
}

func isStringUppercase(s string) bool {
	for _, c := range s {
		if !unicode.IsUpper(c) {
			return false
		}
	}
	return true
}