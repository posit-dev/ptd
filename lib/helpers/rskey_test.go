package helpers

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRsKeyGenerate(t *testing.T) {
	// Generate two keys to ensure they're different
	key1 := RsKeyGenerate()
	key2 := RsKeyGenerate()

	assert.NotEmpty(t, key1)
	assert.NotEmpty(t, key2)

	// Keys should be unique
	assert.NotEqual(t, key1, key2)

	// Keys should follow the expected hex string pattern (a sequence of hexadecimal characters)
	// This regex matches hex strings of any length
	hexPattern := "^[0-9a-fA-F]+$"
	assert.Regexp(t, hexPattern, key1)
	assert.Regexp(t, hexPattern, key2)
}
