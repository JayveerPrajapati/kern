package index

import "regexp"

// Per-language-family declaration rules and keyword sets. Each family shares a
// single rules slice and keyword set across all its languages (e.g. js is used
// for both javascript and typescript). These are referenced by the specs map
// built in init() (foreign.go).

var js = []declRule{
	{kind: "class", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+(?:default\s+)?)?class\s+(` + identRe + `)`)},
	{kind: "interface", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?interface\s+(` + identRe + `)`)},
	{kind: "enum", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?enum\s+(` + identRe + `)`)},
	{kind: "type", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?type\s+(` + identRe + `)\s*=`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=\s*(?:async\s+)?\(`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=\s*(?:async\s+)?` + identRe + `\s*=>`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=\s*(?:async\s+)?function`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s*\*?\s*(` + identRe + `)\s*(?:<[^>]*>)?\s*\(`)},
	{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:get|set|static|async)\s+)*(?:#)?(` + identRe + `)\s*(?:<[^>]*>)?\s*\(.*\)\s*(?::[^{}]*)?\{`)},
	{kind: "const", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=`)},
}

var jsKw = kwSet("if", "for", "while", "switch", "catch", "return", "do", "case", "function",
	"class", "new", "delete", "typeof", "instanceof", "throw", "try", "with", "else",
	"in", "of", "void", "yield", "await", "super", "this", "extends", "implements",
	"interface", "type", "enum", "import", "export", "from", "as", "async", "static",
	"get", "set", "const", "let", "var", "finally", "default", "null", "undefined",
	"true", "false")

var python = []declRule{
	{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)},
}

var pyKw = kwSet("if", "elif", "else", "for", "while", "with", "and", "or", "not", "in",
	"is", "lambda", "def", "class", "return", "import", "from", "assert", "del",
	"raise", "except", "finally", "pass", "break", "continue", "as", "global",
	"nonlocal", "yield", "try", "match", "case", "None", "True", "False")

var rust = []declRule{
	{kind: "struct", re: regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)`)},
	{kind: "enum", re: regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)`)},
	{kind: "trait", re: regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)`)},
	{kind: "impl", re: regexp.MustCompile(`^\s*(?:pub\s+)?impl\s+(?:<[^>]*>\s*)?([A-Za-z_]\w*)`)},
	{kind: "type", re: regexp.MustCompile(`^\s*(?:pub\s+)?type\s+([A-Za-z_]\w*)\s*=`)},
	{kind: "const", re: regexp.MustCompile(`^\s*(?:pub\s+)?const\s+([A-Za-z_]\w*)\s*:`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*(?:<[^>]*>\s*)?\(`)},
}

var rsKw = kwSet("if", "else", "for", "while", "match", "loop", "fn", "return", "let",
	"mut", "const", "static", "struct", "enum", "trait", "impl", "use", "mod", "pub",
	"move", "as", "in", "where", "async", "await", "dyn", "ref", "unsafe", "break",
	"continue", "true", "false", "None", "Some", "Ok", "Err")

var cfam = []declRule{
	{kind: "struct", re: regexp.MustCompile(`^\s*(?:typedef\s+)?struct\s+([A-Za-z_]\w*)`)},
	{kind: "enum", re: regexp.MustCompile(`^\s*(?:typedef\s+)?enum\s+([A-Za-z_]\w*)`)},
	{kind: "union", re: regexp.MustCompile(`^\s*(?:typedef\s+)?union\s+([A-Za-z_]\w*)`)},
	{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
	{kind: "method", isDef: true, recv: 1, re: regexp.MustCompile(`^\s*(?:(?:inline|static|virtual|constexpr)\s+)*(?:[A-Za-z_][\w<>&*\s]*\s+)?([A-Za-z_]\w*)::([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:override\s*)?(?:noexcept\s*)?(?:\s*->\s*[^{]+?)?\s*\{`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:inline\s+|static\s+|virtual\s+|constexpr\s+|extern\s+"[^"]*"\s+)*[A-Za-z_][\w:<>*&\s]*\s+)?([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:override\s*)?(?:noexcept\s*)?(?:\s*->\s*[^{]+?)?\s*\{`)},
}

var cfamKw = kwSet("if", "else", "for", "while", "switch", "return", "sizeof", "do",
	"case", "try", "catch", "throw", "new", "delete", "typeof", "const", "static",
	"void", "struct", "union", "enum", "class", "namespace", "using", "template",
	"typename", "continue", "break", "goto", "true", "false", "NULL", "nullptr",
	"int", "char", "float", "double", "long", "short", "unsigned", "signed",
	"bool", "auto", "register", "extern", "inline", "virtual", "override",
	"public", "private", "protected", "this")

var java = []declRule{
	{kind: "class", re: regexp.MustCompile(`^\s*(?:public|private|protected|abstract|final|sealed)?\s*class\s+([A-Za-z_]\w*)`)},
	{kind: "interface", re: regexp.MustCompile(`^\s*(?:public|private|protected|abstract)?\s*interface\s+([A-Za-z_]\w*)`)},
	{kind: "enum", re: regexp.MustCompile(`^\s*(?:public|private|protected)?\s*enum\s+([A-Za-z_]\w*)`)},
	{kind: "record", re: regexp.MustCompile(`^\s*(?:public|private|protected|final)?\s*record\s+([A-Za-z_]\w*)`)},
	// Method with body (ending with {) — class methods and default interface methods.
	{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|strictfp)\s+)*[A-Za-z_][\w<>\[\].,?\s]*?\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:throws\s+[\w.,\s]+)?\{`)},
	// Interface method declaration (ending with ;) — abstract methods without body.
	{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|strictfp)\s+)*[A-Za-z_][\w<>\[\].,?\s]*?\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:throws\s+[\w.,\s]+)?\s*;`)},
	{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected)?\s*([A-Za-z_]\w*)\s*\([^;{}]*\)\s*\{`)},
}

var javaKw = kwSet("if", "else", "for", "while", "switch", "return", "new", "throw",
	"try", "catch", "finally", "synchronized", "instanceof", "case", "do",
	"continue", "break", "assert", "void", "this", "super", "class", "interface",
	"enum", "extends", "implements", "package", "import", "static", "final",
	"abstract", "public", "private", "protected", "null", "true", "false",
	"int", "long", "short", "byte", "char", "float", "double", "boolean")

var csharp = []declRule{
	{kind: "class", re: regexp.MustCompile(`^\s*(?:public|internal|sealed|abstract|partial|static|readonly|file)?\s*(?:class|interface|record|struct|enum)\s+([A-Za-z_]\w*)`)},
	{kind: "prop", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|readonly|virtual|override|new|async)?\s*[A-Za-z_][\w<>?\[\],.\s]*\s+([A-Za-z_]\w*)\s*\{\s*(?:get|set|init)`)},
	{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|virtual|override|async|new|extern|partial|sealed)?\s*[A-Za-z_][\w<>?\[\],.\s]*\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*\{`)},
	{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*([A-Za-z_]\w*)\s*\([^;{}]*\)\s*\{`)},
}

var csKw = kwSet("if", "else", "for", "foreach", "while", "switch", "return", "using",
	"namespace", "new", "typeof", "throw", "try", "catch", "finally", "lock",
	"async", "await", "as", "is", "out", "ref", "var", "delegate", "event",
	"continue", "break", "goto", "null", "true", "false", "void", "class",
	"interface", "struct", "enum", "record", "base", "this")

var ruby = []declRule{
	{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*(?:::[A-Za-z_]\w*)*)`)},
	{kind: "module", re: regexp.MustCompile(`^\s*module\s+([A-Za-z_]\w*(?:::[A-Za-z_]\w*)*)`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*def\s+(?:self\.)?([A-Za-z_]\w*[!?=]?)`)},
}

var rubyKw = kwSet("if", "unless", "while", "until", "for", "case", "when", "and", "or",
	"not", "def", "class", "module", "end", "begin", "rescue", "ensure", "return",
	"raise", "yield", "do", "then", "else", "elsif", "break", "next", "redo",
	"retry", "true", "false", "nil", "self", "super", "defined")

var php = []declRule{
	{kind: "class", re: regexp.MustCompile(`^\s*(?:abstract|final|readonly)?\s*class\s+([A-Za-z_]\w*)`)},
	{kind: "interface", re: regexp.MustCompile(`^\s*interface\s+([A-Za-z_]\w*)`)},
	{kind: "trait", re: regexp.MustCompile(`^\s*trait\s+([A-Za-z_]\w*)`)},
	{kind: "enum", re: regexp.MustCompile(`^\s*enum\s+([A-Za-z_]\w*)`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract)\s+)*function\s+([A-Za-z_]\w*)\s*\(`)},
	{kind: "const", re: regexp.MustCompile(`^\s*const\s+([A-Za-z_]\w*)\s*=`)},
}

var phpKw = kwSet("if", "else", "elseif", "for", "foreach", "while", "switch", "return",
	"new", "throw", "try", "catch", "finally", "case", "function", "class",
	"interface", "trait", "use", "namespace", "include", "include_once", "require",
	"require_once", "echo", "print", "continue", "break", "goto", "declare",
	"isset", "unset", "empty", "null", "true", "false", "public", "private",
	"protected", "static", "final", "abstract", "global", "list", "array",
	"match", "fn", "enum")

var shell = []declRule{
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{`)},
	{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*function\s+([A-Za-z_]\w*)\s*\{`)},
}

var shKw = kwSet("if", "then", "while", "until", "for", "case", "do", "done", "fi",
	"esac", "function", "in", "select", "elif", "else", "time")

var css = []declRule{
	{kind: "class", re: regexp.MustCompile(`^\s*\.([A-Za-z_][\w-]*)`)},
	{kind: "const", re: regexp.MustCompile(`^\s*#([A-Za-z_][\w-]*)`)},
	{kind: "func", re: regexp.MustCompile(`^\s*@keyframes\s+([A-Za-z_][\w-]*)`)},
	{kind: "prop", re: regexp.MustCompile(`(--[A-Za-z_][\w-]*)\s*:`)},
}

var cssKw = kwSet("import", "media", "supports", "font-face", "layer", "container",
	"scope", "property", "counter-style", "charset", "namespace", "page")

var htmlRules = []declRule{
	{kind: "const", re: regexp.MustCompile(`\bid="([A-Za-z_][\w-]*)"`)},
}

var htmlKw = kwSet()

var markdown = []declRule{
	{kind: "heading", re: regexp.MustCompile(`^(#{1,6})\s+(.+)$`)},
}

var mdKw = kwSet()

var jsonRules = []declRule{
	{kind: "prop", re: regexp.MustCompile(`"([A-Za-z_$][\w$.-]*)"\s*:`)},
}

var jsonKw = kwSet()

var yaml = []declRule{
	{kind: "prop", re: regexp.MustCompile(`^([A-Za-z_][\w-]*):`)},
}

var yamlKw = kwSet()
