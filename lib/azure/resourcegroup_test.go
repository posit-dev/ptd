package azure

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestFormatTagKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "slash is replaced with colon",
			in:   "posit.team/environment",
			want: "posit.team:environment",
		},
		{
			name: "multiple slashes are all replaced",
			in:   "a/b/c",
			want: "a:b:c",
		},
		{
			name: "key without slash is unchanged",
			in:   "CostCenter",
			want: "CostCenter",
		},
		{
			name: "empty key is unchanged",
			in:   "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, FormatTagKey(tt.in))
		})
	}
}
