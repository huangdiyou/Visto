package media

import (
	"errors"
	"testing"
)

func TestParseProbeJSONNormalizesVideoMetadata(t *testing.T) {
	result, err := parseProbeJSON([]byte(`{
		"streams": [
			{
				"codec_type": "video",
				"codec_name": "h264",
				"width": 1920,
				"height": 1080,
				"avg_frame_rate": "30000/1001",
				"side_data_list": [{"rotation": -90}]
			},
			{"codec_type": "audio", "codec_name": "aac"}
		],
		"format": {
			"format_name": "mov,mp4,m4a,3gp,3g2,mj2",
			"format_long_name": "QuickTime / MOV",
			"duration": "12.345678",
			"bit_rate": "8000000"
		}
	}`), ProbeHint{ObjectKey: "cut.mov", MIMEType: "video/quicktime"})
	if err != nil {
		t.Fatalf("parse probe JSON: %v", err)
	}

	if result.MediaType != "video" ||
		value(result.VideoCodec) != "h264" ||
		value(result.AudioCodec) != "aac" ||
		value(result.Width) != 1920 ||
		value(result.Height) != 1080 ||
		value(result.DurationUS) != 12_345_678 ||
		value(result.RotationDegrees) != 270 {
		t.Fatalf("unexpected normalized result: %#v", result)
	}
	if result.FrameRate == nil || *result.FrameRate < 29.96 ||
		*result.FrameRate > 29.98 {
		t.Fatalf("unexpected frame rate: %#v", result.FrameRate)
	}
}

func TestParseProbeJSONUsesImageHint(t *testing.T) {
	result, err := parseProbeJSON([]byte(`{
		"streams": [{
			"codec_type": "video",
			"codec_name": "webp",
			"width": 800,
			"height": 600,
			"avg_frame_rate": "0/0"
		}],
		"format": {"format_name": "webp_pipe"}
	}`), ProbeHint{ObjectKey: "poster.webp", MIMEType: "image/webp"})
	if err != nil {
		t.Fatalf("parse image probe JSON: %v", err)
	}
	if result.MediaType != "image" ||
		value(result.Width) != 800 ||
		value(result.Height) != 600 {
		t.Fatalf("unexpected image result: %#v", result)
	}
}

func TestParseProbeJSONRejectsInvalidOutput(t *testing.T) {
	_, err := parseProbeJSON([]byte(`not-json`), ProbeHint{})
	if !errors.Is(err, ErrProbeFailed) {
		t.Fatalf("expected probe failure, got %v", err)
	}
}

func value[T comparable](pointer *T) T {
	if pointer == nil {
		var zero T
		return zero
	}
	return *pointer
}
