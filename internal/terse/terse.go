// Package terse implements deterministic LLM output compression: it strips
// filler, pleasantries and hedge language from a model's prose response while
// preserving code blocks, lists, errors and technical terms. No LLM involved,
// byte-for-byte reproducible.
package terse

import (
	"strings"
	"unicode"
)

// filler lines are dropped verbatim (after trimming punctuation).
var fillerExact = map[string]bool{
	"certainly":             true,
	"certainly!":            true,
	"sure":                  true,
	"sure!":                 true,
	"sure thing":            true,
	"absolutely":            true,
	"absolutely!":           true,
	"of course":             true,
	"of course!":            true,
	"no problem":            true,
	"you're welcome":        true,
	"you are welcome":       true,
	"happy to help":         true,
	"glad to help":          true,
	"my pleasure":           true,
	"thanks":                true,
	"thanks!":               true,
	"thank you":             true,
	"thanks for asking":     true,
	"great question":        true,
	"great question!":       true,
	"good question":         true,
	"here you go":           true,
	"hope this helps":       true,
	"hope this helps!":      true,
	"let me know if you":    true,
	"let me know if":        true,
	"let me know what":      true,
	"let me know":           true,
	"feel free to ask":      true,
	"please don't hesitate": true,
	"don't hesitate":        true,
	"don't hesitate to ask": true,
	"best regards":          true,
	"regards":               true,
	"happy coding":          true,
	"good luck":             true,
	"good luck!":            true,
	"let's get started":     true,
	"lets get started":      true,
	"here's how":            true,
	"here is how":           true,
	"here's what you":       true,
	"here is what you":      true,
	"in summary":            true,
	"in conclusion":         true,
	"bottom line":           true,
	"i hope":                true,
	"i hope this":           true,
	"enjoy":                 true,
	"you got it":            true,
	"you bet":               true,
	"no worries":            true,
	"all good":              true,
	"got it":                true,
	"got it!":               true,
	"right":                 true,
	"right!":                true,
	"exactly":               true,
	"exactly!":              true,
	"nice":                  true,
	"nice!":                 true,
	"well done":             true,
	"great job":             true,
}

// hedgePrefixes mark a line as expendable filler when it *starts* with one of
// these (case-insensitive).
var fillerPrefixes = []string{
	"let me know if",
	"let me know what",
	"feel free to",
	"please don't hesitate",
	"don't hesitate",
	"i think",
	"i believe",
	"i feel",
	"i'm not sure",
	"i am not sure",
	"in my opinion",
	"as far as i",
	"it seems",
	"it appears",
	"seems like",
	"note that",
	"please note",
	"it's worth noting",
	"it is worth noting",
	"it's important to note",
	"it is important to note",
	"one thing to note",
	"as a reminder",
	"just to be clear",
	"to be clear",
	"in other words",
	"in short",
	"long story short",
	"to summarize",
	"to sum up",
	"in a nutshell",
	"at the end of the day",
	"needless to say",
	"obviously",
	"of course,",
	"certainly,",
	"interestingly",
	"unfortunately,",
	"unfortunately,",
	"luckily",
	"hopefully",
	"anyway,",
	"anyhow,",
	"regardless,",
	"as i said",
	"as mentioned",
	"as mentioned earlier",
	"as i mentioned",
	"again,",
	"additionally,",
	"moreover,",
	"furthermore,",
	"also, remember",
	"remember,",
	"keep in mind",
	"please remember",
	"it goes without saying",
	"no offense",
	"with that said",
	"that said,",
	"having said that",
	"on a side note",
	"by the way",
	"btw,",
	"fyi,",
	"for reference",
	"for your reference",
	"for context,",
	"just a heads up",
	"quick note",
	"quick tip",
	"pro tip",
	"one more thing",
	"last but not least",
	"first and foremost",
	"i hope this helps",
	"hope that helps",
	"hope it helps",
	"happy to clarify",
	"happy to elaborate",
	"let me elaborate",
	"let me explain",
	"let me walk you",
	"let me break it down",
	"the short answer is",
	"the short version is",
	"the long and short",
	"bottom line:",
}

// Compress strips filler from text. It returns the compressed text plus the
// number of dropped lines (for stats). Code fences are preserved verbatim.
func Compress(text string) (string, int) {
	lines := strings.Split(text, "\n")
	var out []string
	dropped := 0
	inFence := false
	for _, raw := range lines {
		trimmed := strings.TrimRightFunc(raw, unicode.IsSpace)

		if isFenceLine(trimmed) {
			inFence = !inFence
			out = append(out, raw)
			continue
		}
		if inFence {
			out = append(out, raw)
			continue
		}

		clean := strings.TrimSpace(trimmed)
		if clean == "" {
			out = append(out, "")
			continue
		}
		if isFiller(clean) {
			dropped++
			continue
		}
		// Strip inline filler phrases from prose lines (preserving code
		// fences). stripInlineFiller only removes filler phrases and keeps
		// any payload, so it is safe to run on payload lines too: a short
		// line like "Sure! The dispatch is in server.go. Hope that helps!"
		// keeps its payload while losing the filler.
		stripped := stripInlineFiller(clean)
		out = append(out, raw[:len(trimmed)-len(clean)]+stripped)
	}

	// Collapse runs of blank lines to a single blank line, then trim leading
	// and trailing blanks.
	var collapsed []string
	prevBlank := false
	for _, l := range out {
		blank := strings.TrimSpace(l) == ""
		if blank && prevBlank {
			continue
		}
		collapsed = append(collapsed, l)
		prevBlank = blank
	}
	for len(collapsed) > 0 && strings.TrimSpace(collapsed[0]) == "" {
		collapsed = collapsed[1:]
	}
	for len(collapsed) > 0 && strings.TrimSpace(collapsed[len(collapsed)-1]) == "" {
		collapsed = collapsed[:len(collapsed)-1]
	}
	return strings.Join(collapsed, "\n"), dropped
}

// StripPromptFluff removes unambiguous conversational filler from a prompt:
// greetings, thanks, politeness clichés and hedges that cannot carry technical
// payload. It is intentionally narrower than Compress: general hedge prefixes
// ("note that", "i think") are NOT used here, because in a prompt they often
// prefix a real instruction. Payload lines (paths, code, "file:line", braces)
// always survive via carriesPayload.
func StripPromptFluff(text string) (string, int) {
	lines := strings.Split(text, "\n")
	var out []string
	dropped := 0
	inFence := false
	for _, raw := range lines {
		trimmed := strings.TrimRightFunc(raw, unicode.IsSpace)
		if isFenceLine(trimmed) {
			inFence = !inFence
			out = append(out, raw)
			continue
		}
		if inFence {
			out = append(out, raw)
			continue
		}
		clean := strings.TrimSpace(trimmed)
		if clean == "" {
			out = append(out, "")
			continue
		}
		if isPromptFluff(clean) {
			dropped++
			continue
		}
		// Keep the leading indentation, drop trailing whitespace (see Compress).
		out = append(out, raw[:len(trimmed)-len(clean)]+clean)
	}
	// Collapse blank runs and trim leading/trailing blanks (same as Compress).
	var collapsed []string
	prevBlank := false
	for _, l := range out {
		blank := strings.TrimSpace(l) == ""
		if blank && prevBlank {
			continue
		}
		collapsed = append(collapsed, l)
		prevBlank = blank
	}
	for len(collapsed) > 0 && strings.TrimSpace(collapsed[0]) == "" {
		collapsed = collapsed[1:]
	}
	for len(collapsed) > 0 && strings.TrimSpace(collapsed[len(collapsed)-1]) == "" {
		collapsed = collapsed[:len(collapsed)-1]
	}
	result := strings.Join(collapsed, "\n")
	// Never hand back an empty prompt: stripping filler must not delete the
	// actual request ("So basically, ... let me help you with that.").
	if dropped > 0 && strings.TrimSpace(result) == "" {
		return text, 0
	}
	return result, dropped
}

// promptFluffExact are whole lines that are pure conversational filler.
var promptFluffExact = map[string]bool{
	"thanks": true, "thank you": true, "thanks a lot": true,
	"thanks so much": true, "thank you very much": true, "thank you so much": true,
	"thanks in advance": true, "thank you in advance": true, "tia": true,
	"thanks again": true, "thanks for reading": true, "thanks for your time": true,
	"thanks for your help": true, "thank you for your help": true,
	"thanks for the help": true, "appreciate it": true,
	"i appreciate your help": true, "i really appreciate it": true,
	"i'd really appreciate it": true, "i would really appreciate it": true,
	"any help would be appreciated": true, "any help is appreciated": true,
	"please help": true, "please help me": true, "please help me out": true,
	"help me please": true, "i need help": true, "i need your help": true,
	"can you help me": true, "could you help me": true, "can you help me out": true,
	"could you help me out": true, "would you be able to help": true,
	"sorry": true, "sorry!": true, "sorry for the long message": true,
	"sorry for the wall of text": true, "sorry for being so verbose": true,
	"apologies for the long message": true, "apologies for the verbosity": true,
	"i apologize for the long message": true, "great": true, "great!": true,
	"awesome": true, "perfect": true, "excellent": true, "nice": true,
	"nice!": true, "sounds good": true, "ok": true, "okay": true,
	"got it": true, "got it!": true, "understood": true, "noted": true,
	"cheers": true, "regards": true, "best": true, "best regards": true,
	"have a great day": true, "have a nice day": true, "take care": true,
}

// promptFluffPrefixes are line-openers that are unambiguous conversational
// filler in a prompt context. Lines carrying payload chars are already kept by
// carriesPayload, which runs first.
var promptFluffPrefixes = []string{
	"hello", "hi ", "hi,", "hey ", "hey,", "good morning", "good afternoon",
	"good evening", "i hope you", "i hope this", "hope you", "hope this",
	"i was wondering", "i was just wondering", "wondering if you",
	"i would like to ask", "i have a question", "i have a quick question",
	"quick question", "just a quick question", "so basically,",
	"i've been stuck", "i have been stuck", "i've been trying",
	"i am stuck", "i'm stuck", "i'm getting frustrated", "i am getting frustrated",
	"i'm pretty frustrated", "i am pretty frustrated", "i'm honestly stuck",
	"please take a look", "can you take a look", "could you take a look",
	"please look into", "can you look into", "could you look into",
	"let me give you", "let me provide", "let me explain", "let me walk you",
	"let me break it down", "just to give you", "for context,",
	"to be honest", "honestly,", "sorry for", "apologies for",
	"thanks in advance", "thanks so much", "thanks again", "thank you for your time",
	"hoping you can help", "hoping you can", "i'm hoping you", "i am hoping you",
	"i really hope you", "any help you can provide", "if it's not too much trouble",
	"i don't want to bother you", "sorry to bother you", "sorry to bug you",
	"this may be a silly question", "this might be a dumb question",
	"i might be wrong but", "correct me if i'm wrong",
}

// isPromptFluff reports whether a cleaned prompt line is expendable chatter.
func isPromptFluff(s string) bool {
	if carriesPayload(s) {
		return false
	}
	lower := strings.ToLower(s)
	lower = strings.TrimRight(lower, " .!?")
	if promptFluffExact[lower] {
		return true
	}
	for _, p := range promptFluffPrefixes {
		if strings.HasPrefix(lower, p) {
			// Unambiguous conversational opener — the rest of the line is
			// padded prose, not a technical instruction. StripPromptFluff
			// guards the overall result so a prompt is never reduced to
			// empty when a filler-only line is the entire input.
			return true
		}
	}
	// A short line made entirely of filler words ("Sure! Great question.").
	return isFillerWords(lower)
}

// inlineFillerPhrases are conversational filler phrases that wrap a real
// sentence without carrying technical meaning. They are stripped from the
// beginning of a line and after commas in prose (never inside code fences).
var inlineFillerPhrases = []string{
	"well, ", "well ", "so, ", "so ",
	"i think that, ", "i think that ", "i think, ", "i think ",
	"you know, ", "you know ",
	"basically just ", "basically, ", "basically ",
	"i mean, ", "i mean ",
	"sort of ", "kind of ",
	"to be honest, ", "honestly, ", "honestly ",
	"for what it's worth, ", "for what its worth, ",
	"at the end of the day, ",
	"i believe that, ", "i believe that ", "i believe, ", "i believe ",
	"it seems like, ", "it seems like ", "it seems that, ", "it seems that ",
	"it appears that, ", "it appears that ",
	"as you can see, ", "as you know, ",
	"needless to say, ", "obviously, ", "obviously ",
	"of course, ", "of course ",
	"certainly, ", "certainly ",
	"hopefully, ", "hopefully ",
	"fortunately, ", "fortunately ",
	"unfortunately, ", "unfortunately ",
	"interestingly, ", "interestingly ",
	"naturally, ", "naturally ",
	"clearly, ", "clearly ",
	"indeed, ", "indeed ",
	"just ",   // "just returns" → "returns"
	"really ", // "really good" → "good"
	"very ",   // "very fast" → "fast"
	"sure! ", "sure, ", "sure ",
	"i'd be happy to help you with that. ",
	"i'd be happy to help you with that.",
	"i'd be happy to help", "i'd be happy to ",
	"i'd be glad to help", "i'd be glad to ",
	"hope that helps! ", "hope that helps!",
	"hope that helps", "hope this helps! ", "hope this helps!",
	"hope this helps",
	"let me know if you need anything else", "let me know if you need anything",
}

// stripInlineFiller removes conversational filler phrases from a prose line.
// It strips phrases from the beginning of the line (looped — so "Well, I
// think that, you know, ..." strips all three openers), after commas, and
// after sentence-ending periods. Code fences and payload lines are already
// guarded by the caller.
func stripInlineFiller(s string) string {
	lower := strings.ToLower(s)
	changed := false
	// Strip from the beginning of the line, looping so consecutive fillers
	// are all removed ("Well, I think that, you know, ..." → "").
	for {
		found := false
		for _, phrase := range inlineFillerPhrases {
			if strings.HasPrefix(lower, phrase) {
				s = s[len(phrase):]
				lower = strings.ToLower(s)
				changed = true
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	// Strip after commas: ", you know, " → ", "
	for _, phrase := range inlineFillerPhrases {
		target := ", " + phrase
		for strings.Contains(lower, target) {
			idx := strings.Index(lower, target)
			s = s[:idx] + ", " + s[idx+len(target):]
			lower = strings.ToLower(s)
			changed = true
		}
	}
	// Strip after sentence-ending periods: ". Basically just " → ". "
	for _, phrase := range inlineFillerPhrases {
		target := ". " + phrase
		for strings.Contains(lower, target) {
			idx := strings.Index(lower, target)
			s = s[:idx] + ". " + s[idx+len(target):]
			lower = strings.ToLower(s)
			changed = true
		}
	}
	// Strip trailing filler phrases (e.g., " Hope that helps!" at end of line)
	for {
		found := false
		for _, phrase := range inlineFillerPhrases {
			if strings.HasSuffix(lower, phrase) {
				s = s[:len(s)-len(phrase)]
				lower = strings.ToLower(s)
				changed = true
				found = true
				break
			}
		}
		if !found {
			break
		}
	}
	if !changed {
		return s
	}
	// Clean up: remove leading comma if the line now starts with one,
	// collapse double commas, and trim leading/trailing whitespace.
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, ", ")
	s = strings.TrimPrefix(s, ",")
	// Collapse ",, " → ", "
	for strings.Contains(s, ", , ") {
		s = strings.ReplaceAll(s, ", , ", ", ")
	}
	for strings.Contains(s, ",,") {
		s = strings.ReplaceAll(s, ",,", ",")
	}
	// Capitalize the first letter if the line now starts with a lowercase
	// letter (the filler was stripping the sentence opener).
	if len(s) > 0 && s[0] >= 'a' && s[0] <= 'z' {
		s = string(s[0]-'a'+'A') + s[1:]
	}
	return s
}

func isFenceLine(s string) bool {
	t := strings.TrimSpace(s)
	return strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~")
}

// fillerWord is a single token that never carries technical meaning on its
// own. A line made entirely of these (e.g. "Sure! Great question.") is dropped.
var fillerWord = map[string]bool{
	"sure": true, "certainly": true, "absolutely": true, "definitely": true,
	"great": true, "good": true, "question": true, "answer": true, "of": true,
	"course": true, "no": true, "problem": true, "thanks": true, "thank": true,
	"you": true, "you're": true, "welcome": true, "happy": true, "glad": true,
	"help": true, "hope": true, "this": true, "helps": true, "yes": true,
	"ok": true, "okay": true, "right": true, "nice": true, "cool": true,
	"awesome": true, "perfect": true, "exactly": true, "correct": true,
	"done": true, "got": true, "it": true, "understood": true, "noted": true,
	"enjoy": true, "bye": true, "cheers": true, "regards": true, "best": true,
	"hello": true, "hi": true, "hey": true, "there": true, "anytime": true,
	"gotcha": true, "yep": true, "yeah": true, "yup": true, "thx": true,
	"indeed": true, "agreed": true, "excellent": true, "wonderful": true,
	"interesting": true, "lovely": true, "splendid": true,
	// Common function words / polite padding that appear after a fluff prefix
	// ("I hope you're well.", "Thanks so much in advance!").
	"re": true, "well": true, "at": true, "the": true, "a": true, "an": true,
	"in": true, "on": true, "for": true, "and": true, "with": true, "really": true,
	"very": true, "much": true, "me": true, "my": true, "your": true, "our": true,
	"just": true, "now": true, "then": true, "here": true, "so": true, "too": true,
	"also": true, "please": true, "take": true, "look": true, "advance": true,
	"asking": true, "tell": true, "give": true, "know": true, "let": true,
	"want": true, "wanted": true, "say": true, "ask": true, "could": true,
	"would": true, "should": true, "can": true, "will": true, "have": true,
	"has": true, "had": true, "am": true, "is": true, "are": true, "was": true,
	"were": true, "be": true, "been": true, "being": true, "to": true,
	"i": true, "i'm": true, "i've": true, "i'd": true, "we": true, "we're": true,
	"about": true, "from": true, "that": true, "these": true,
	"those": true, "some": true, "any": true, "more": true, "helping": true,
	"code": true,
}

// fillerAck are single-word lines that are unambiguous acknowledgements
// ("Sure.", "Thanks.", "OK."). Function words ("a", "the", "i") never qualify —
// they are only filler in combination with other words.
var fillerAck = map[string]bool{
	"sure": true, "certainly": true, "absolutely": true, "definitely": true,
	"great": true, "good": true, "yes": true, "yeah": true, "yep": true,
	"yup": true, "ok": true, "okay": true, "right": true, "nice": true,
	"cool": true, "awesome": true, "perfect": true, "exactly": true,
	"correct": true, "done": true, "got": true, "it": true, "understood": true,
	"noted": true, "cheers": true, "regards": true, "hello": true, "hi": true,
	"hey": true, "anytime": true, "gotcha": true, "thx": true, "thanks": true,
	"thank": true, "bye": true, "indeed": true, "agreed": true,
	"excellent": true, "wonderful": true, "interesting": true, "lovely": true,
	"splendid": true, "there": true,
}

// isFillerWords reports whether every token in s is a known filler word. A
// single token is only filler if it is an unambiguous acknowledgement.
func isFillerWords(s string) bool {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return r == ' ' || r == ',' || r == '!' || r == '?' || r == '.' || r == '"'
	})
	if len(words) == 0 || len(words) > 6 {
		return false
	}
	clean := func(tok string) string { return strings.Trim(tok, "'-") }
	if len(words) == 1 {
		return fillerAck[clean(words[0])]
	}
	for _, w := range words {
		if !fillerWord[clean(w)] {
			return false
		}
	}
	return true
}

// isFiller reports whether a single cleaned line is expendable prose. A line
// that carries technical payload (contains code-ish punctuation) is never
// dropped.
func isFiller(s string) bool {
	if carriesPayload(s) {
		return false
	}
	lower := strings.ToLower(s)
	lower = strings.TrimRight(lower, " .!?")
	// An action word at the end (e.g. "let's fix it") means real instruction.
	if fillerExact[lower] {
		return true
	}
	for _, p := range fillerPrefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	// Trailing single-word thanks/greetings ("Thanks", "Cheers", "Regards").
	switch lower {
	case "thanks", "thank you", "cheers", "regards", "thanks again",
		"thanks so much", "thank you so much", "have a great day",
		"have a nice day", "take care", "goodbye", "bye", "see you":
		return true
	}
	// A short line composed entirely of greeting/filler words ("Sure! Great
	// question.") is pure small talk — drop it.
	return isFillerWords(lower)
}

// carriesPayload keeps lines that look like code, identifiers, paths, error
// messages or structured content — never drop these.
func carriesPayload(s string) bool {
	if strings.Contains(s, ":") || strings.Contains(s, "=") {
		return true
	}
	if strings.Contains(s, "(") || strings.Contains(s, ")") {
		return true
	}
	if strings.Contains(s, "/") || strings.Contains(s, "\\") || strings.Contains(s, "file") {
		return true
	}
	if strings.ContainsAny(s, "#[]{}<>&$") {
		return true
	}
	// Leading bullets/dashes/numbers: list items are content.
	t := strings.TrimLeft(s, " \t-*•0123456789.")
	if t != "" && len(t) != len(s) && (t == "" || !strings.HasPrefix(t, " ")) {
		return true
	}
	if len(s) > 120 {
		return true // long lines are unlikely to be pure filler
	}
	return false
}
