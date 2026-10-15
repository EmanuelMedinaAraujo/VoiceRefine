package clip

import (
	"encoding/binary"
	"io"
	"os"
	"time"
)

// The recorder writes raw 16-bit mono samples at 16 kHz, the format Voxtype itself feeds its models.
const (
	SampleRate     = 16000
	bytesPerSample = 2
)

// RawDuration is how much audio n bytes of raw samples hold.
func RawDuration(n int64) time.Duration {
	return time.Duration(n/bytesPerSample) * time.Second / SampleRate
}

func rawBytes(d time.Duration) int64 {
	return int64(d * SampleRate / time.Second * bytesPerSample)
}

// RawToWAV wraps the raw recording in a WAV header. When limit is positive, the audio after it is
// left out. It returns the length of the written audio.
func RawToWAV(rawPath, wavPath string, limit time.Duration) (time.Duration, error) {
	raw, err := os.Open(rawPath)
	if err != nil {
		return 0, err
	}
	defer raw.Close()
	st, err := raw.Stat()
	if err != nil {
		return 0, err
	}
	n := st.Size() - st.Size()%bytesPerSample
	if limit > 0 {
		n = min(n, rawBytes(limit))
	}

	tmp := wavPath + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp)
	if err := writeHeader(out, n); err != nil {
		out.Close()
		return 0, err
	}
	if _, err := io.CopyN(out, raw, n); err != nil {
		out.Close()
		return 0, err
	}
	if err := out.Close(); err != nil {
		return 0, err
	}
	return RawDuration(n), os.Rename(tmp, wavPath)
}

func writeHeader(w io.Writer, dataBytes int64) error {
	h := struct {
		Riff          [4]byte
		Size          uint32
		Wave          [4]byte
		Fmt           [4]byte
		FmtSize       uint32
		Format        uint16
		Channels      uint16
		Rate          uint32
		ByteRate      uint32
		BlockAlign    uint16
		BitsPerSample uint16
		Data          [4]byte
		DataSize      uint32
	}{
		[4]byte{'R', 'I', 'F', 'F'}, uint32(36 + dataBytes), [4]byte{'W', 'A', 'V', 'E'},
		[4]byte{'f', 'm', 't', ' '}, 16, 1, 1, SampleRate, SampleRate * bytesPerSample, bytesPerSample, 16,
		[4]byte{'d', 'a', 't', 'a'}, uint32(dataBytes),
	}
	return binary.Write(w, binary.LittleEndian, h)
}
