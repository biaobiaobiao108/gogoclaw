package channels

import (
	"crypto/sha256"
	"encoding/binary"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

var idempotencyNamespace = uuid.MustParse("fd4c9f4e-4aca-4d9f-a7bd-9f46c9f1c9a4")

// StableID derives a platform-safe logical identifier from the durable
// outbound message ID and optional operation parts.
func StableID(parts ...string) string {
	return uuid.NewSHA1(idempotencyNamespace, []byte(strings.Join(parts, "\x00"))).String()
}

// StableNumericID returns a positive decimal identifier suitable for platforms
// that require numeric idempotency values.
func StableNumericID(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	value := binary.BigEndian.Uint64(sum[:8]) & (1<<63 - 1)
	if value == 0 {
		value = 1
	}
	return strconv.FormatUint(value, 10)
}

// StableSequence returns a stable sequence in the range used by QQ's passive
// reply API, where a message ID and sequence together identify a delivery.
func StableSequence(parts ...string) uint32 {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return binary.BigEndian.Uint32(sum[:4])%1_000_000 + 1
}
