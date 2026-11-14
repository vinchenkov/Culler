package boundary

import "testing"

func TestParseVolumeCaseCapabilities(t *testing.T) {
	tests := []struct {
		name    string
		words   volumeCapabilityWords
		want    suffixCaseMode
		wantErr bool
	}{
		{
			name: "case-sensitive valid bit and capability",
			words: volumeCapabilityWords{
				Length:       volumeCapabilityBufferSize,
				Capabilities: [4]uint32{volumeCapFmtCaseSensitive},
				Valid:        [4]uint32{volumeCapFmtCaseSensitive},
			},
			want: suffixCaseSensitive,
		},
		{
			name: "case-insensitive valid bit with capability clear",
			words: volumeCapabilityWords{
				Length: volumeCapabilityBufferSize,
				Valid:  [4]uint32{volumeCapFmtCaseSensitive},
			},
			want: suffixCaseInsensitive,
		},
		{
			name: "missing valid bit is unknown",
			words: volumeCapabilityWords{
				Length:       volumeCapabilityBufferSize,
				Capabilities: [4]uint32{volumeCapFmtCaseSensitive},
			},
			want:    suffixCaseUnknown,
			wantErr: true,
		},
		{
			name: "short kernel payload is unknown",
			words: volumeCapabilityWords{
				Length: volumeCapabilityBufferSize - 1,
				Valid:  [4]uint32{volumeCapFmtCaseSensitive},
			},
			want:    suffixCaseUnknown,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVolumeCaseCapabilities(tt.words)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseVolumeCaseCapabilities() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("parseVolumeCaseCapabilities() = %v, want %v", got, tt.want)
			}
		})
	}
}
