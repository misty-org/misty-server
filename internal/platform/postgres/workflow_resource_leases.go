package db

import (
	"encoding/hex"
	"strings"
)

const resourceLeasePrefix = "resource-lease:"

func resourceLeaseTopic(topic string) bool { return validDigestTopic(topic, resourceLeasePrefix) }
func validDigestTopic(topic, prefix string) bool {
	if !strings.HasPrefix(topic, prefix) || len(topic) != len(prefix)+64 {
		return false
	}
	_, err := hex.DecodeString(topic[len(prefix):])
	return err == nil
}
