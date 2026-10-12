package media

import (
	"sync"
	"testing"
)

// The Owner changes the encoder from an HTTP handler while job workers are
// choosing a plan, so the configuration has to be read as one snapshot.
//
// This test is meaningful under `go test -race`: before the configuration was
// guarded it reported a data race between ApplyEncoderChoice and the readers
// below, on exactly the path the Owner's setting is meant to control.
func TestEncoderConfigurationIsRaceFree(t *testing.T) {
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	profile := VideoProfile{Kind: RenditionProxy}

	var wg sync.WaitGroup
	for worker := 0; worker < 6; worker++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for round := 0; round < 150; round++ {
				_ = processor.AccelerationSettings()
				_ = processor.encoderCandidates()
				_ = processor.encodingPlans(profile)
				_ = processor.softwareVideoEncodingPlan()
			}
		}()
		go func() {
			defer wg.Done()
			for round := 0; round < 150; round++ {
				processor.ApplyEncoderChoice("h264_qsv")
				processor.ApplyEncoderChoice("libx264")
				processor.SetSoftwareEncoder("libopenh264")
				processor.SetAccelerationMode(VideoAccelerationSoftware)
			}
		}()
	}
	wg.Wait()
}

// The readers have to agree with each other: a plan must not be built from a
// software encoder that a concurrent choice already replaced.
func TestEncoderConfigurationSnapshotIsConsistent(t *testing.T) {
	processor := NewFFmpegVideoProcessor("ffmpeg", t.TempDir())
	processor.SetSoftwareEncoder("libx264")

	config := processor.encoderConfig()
	if config.softwareEncoder != "libx264" || config.mode != VideoAccelerationSoftware {
		t.Fatalf("snapshot = %#v, want the configured software encoder", config)
	}

	processor.ApplyEncoderChoice("h264_videotoolbox")
	if settings := processor.AccelerationSettings(); settings.Encoder == "" {
		t.Fatalf("settings = %#v, want an encoder after the choice", settings)
	}
}
