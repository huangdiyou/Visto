package media

import "testing"

func TestApplyEncoderChoicePointsTheProcessorAtTheChosenEncoder(t *testing.T) {
	t.Parallel()

	cases := []struct {
		encoder string
		mode    string
	}{
		{"h264_qsv", VideoAccelerationQSV},
		{"h264_nvenc", VideoAccelerationNVENC},
		{"h264_amf", VideoAccelerationAMF},
		{"h264_videotoolbox", VideoAccelerationVideoToolbox},
		{"libopenh264", VideoAccelerationSoftware},
		{"libx264", VideoAccelerationSoftware},
	}
	for _, testCase := range cases {
		t.Run(testCase.encoder, func(t *testing.T) {
			processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
			if !processor.ApplyEncoderChoice(testCase.encoder) {
				t.Fatalf("ApplyEncoderChoice(%q) was refused", testCase.encoder)
			}
			settings := processor.AccelerationSettings()
			if settings.Mode != testCase.mode || settings.Encoder != testCase.encoder {
				t.Fatalf("settings = %#v, want mode %s encoder %s",
					settings, testCase.mode, testCase.encoder)
			}
		})
	}
}

// A stored name that is no longer recognised must be reported, not silently
// turned into some other encoder.
func TestApplyEncoderChoiceRefusesAnUnknownEncoder(t *testing.T) {
	t.Parallel()

	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.SetAccelerationMode(VideoAccelerationQSV)
	before := processor.AccelerationSettings()

	if processor.ApplyEncoderChoice("h264_bogus") {
		t.Fatal("an unknown encoder must be refused")
	}
	if processor.ApplyEncoderChoice("") {
		t.Fatal("an empty encoder must be refused")
	}
	if after := processor.AccelerationSettings(); after != before {
		t.Fatalf("a refused choice changed the settings: %#v -> %#v", before, after)
	}
}
