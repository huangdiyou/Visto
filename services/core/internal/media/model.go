package media

import "time"

type Metadata struct {
	StorageObjectID   string
	WorkspaceID       string
	Status            string
	SourceFingerprint string
	MediaType         string
	FormatName        *string
	FormatLongName    *string
	DurationUS        *int64
	BitRate           *int64
	Width             *int
	Height            *int
	RotationDegrees   *int
	FrameRate         *float64
	VideoCodec        *string
	AudioCodec        *string
	RawMetadataJSON   *string
	ErrorCode         *string
	ErrorMessage      *string
	ProbedAt          time.Time
}

type ProbeHint struct {
	ObjectKey string
	MIMEType  string
}

type ProbeResult struct {
	MediaType       string
	FormatName      *string
	FormatLongName  *string
	DurationUS      *int64
	BitRate         *int64
	Width           *int
	Height          *int
	RotationDegrees *int
	FrameRate       *float64
	VideoCodec      *string
	AudioCodec      *string
	RawMetadataJSON string
}

type ProbeBatch struct {
	RootID    string
	Available int
	Attempted int
	Succeeded int
	Failed    int
	Skipped   int
	Remaining int
	Items     []Metadata
}
