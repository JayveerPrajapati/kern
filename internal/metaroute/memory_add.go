package metaroute

import "strings"

// classifyMemoryAdd routes "<add verb> ... lesson|memory: <text>" to
// kern_memory action=add. kern_memory is not on the default advertised
// surface, so without this arm an agent can recall project memory through
// kern_meta but never write it. A request with no colon payload keeps the
// recall route ("remember a lesson learned about X" is a recall).
func classifyMemoryAdd(request string) (string, map[string]any, bool) {
	i := strings.Index(request, ":")
	if i < 0 {
		return "", nil, false
	}
	payload := strings.TrimSpace(request[i+1:])
	if payload == "" {
		return "", nil, false
	}
	var verb, noun bool
	for _, w := range strings.Fields(strings.ToLower(request[:i])) {
		switch strings.Trim(w, ".,;!?()\"'") {
		case "remember", "save", "store", "record", "add":
			verb = true
		case "lesson", "lessons", "memory", "memories":
			noun = true
		}
	}
	if !verb || !noun {
		return "", nil, false
	}
	return "kern_memory", map[string]any{"action": "add", "lesson": payload}, true
}
