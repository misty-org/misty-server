package capabilities

import (
	"regexp"
	"strconv"
	"strings"
)

const RoutineAgentMaxCalls = 40

var routineModelCallID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,120}$`)

func RoutineAgentCallNamespace(callID string) (string, bool) {
	namespace, suffix, ok := strings.Cut(callID, ":")
	return namespace, ok && ValidID(namespace) && routineModelCallID.MatchString(suffix)
}
func RoutineAgentModelNode(nodeID string) (namespace string, turn int, ok bool) {
	rest, found := strings.CutPrefix(nodeID, "model:routine:")
	if !found {
		return "", 0, false
	}
	namespace, number, found := strings.Cut(rest, ":")
	if !found || !ValidID(namespace) {
		return "", 0, false
	}
	turn, err := strconv.Atoi(number)
	return namespace, turn, err == nil && turn >= 1 && turn <= 20 && strconv.Itoa(turn) == number
}
