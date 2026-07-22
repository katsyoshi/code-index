package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

const (
	indexStatusIndexed = "indexed"
	indexStatusSkipped = "skipped"

	encodingSourceUTF8        = "utf8"
	encodingSourceBOM         = "bom"
	encodingSourceDeclaration = "declaration"
	encodingSourceFallback    = "fallback"

	skipReasonEncodingUnknown       = "encoding_unknown"
	skipReasonEncodingConflict      = "encoding_conflict"
	skipReasonConversionFailed      = "conversion_failed"
	skipReasonTranscoderUnavailable = "transcoder_unavailable"
)

var (
	errNotText               = errors.New("not text")
	errTranscoderUnavailable = errors.New("iconv command not found")
	pythonEncodingPattern    = regexp.MustCompile(`coding[=:][ \t]*([-A-Za-z0-9_.]+)`)
	rubyEncodingPattern      = regexp.MustCompile(`(?:coding|encoding):[ \t]+([-A-Za-z0-9_.]+)`)
	iconvTranscode           = runIconv
)

type decodedSource struct {
	text           string
	indexStatus    string
	sourceEncoding string
	encodingSource string
	transcoded     bool
	skipReason     string
}

func validateEncodingFallbacks(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			return nil, errors.New("encoding names must not be empty")
		}
		key := strings.ToLower(value)
		if seen[key] {
			return nil, fmt.Errorf("duplicate encoding %q", value)
		}
		seen[key] = true
		result = append(result, value)
	}
	return result, nil
}

func decodeSource(data []byte, language string, fallbacks []string) (decodedSource, error) {
	if encoding, bomSize := unicodeBOM(data); encoding != "" {
		text, err := decodeNamed(data[bomSize:], encoding)
		if err != nil {
			return skippedSource(encoding, encodingSourceBOM, skipReasonConversionFailed), nil
		}
		if strings.ContainsRune(text, '\x00') {
			return decodedSource{}, errNotText
		}
		if declared := declaredEncoding([]byte(text), language); declared != "" && !equivalentEncoding(declared, encoding) {
			return skippedSource(encoding, encodingSourceBOM, skipReasonEncodingConflict), nil
		}
		return indexedSource(text, encoding, encodingSourceBOM, encoding != "UTF-8"), nil
	}
	if bytesContainNUL(data) {
		return decodedSource{}, errNotText
	}

	if declared := declaredEncoding(data, language); declared != "" {
		text, err := decodeNamed(data, declared)
		if errors.Is(err, errTranscoderUnavailable) {
			return skippedSource(declared, encodingSourceDeclaration, skipReasonTranscoderUnavailable), nil
		}
		if err != nil {
			return skippedSource(declared, encodingSourceDeclaration, skipReasonConversionFailed), nil
		}
		return indexedSource(text, canonicalUnicodeEncoding(declared), encodingSourceDeclaration, !isUTF8Encoding(declared)), nil
	}

	if utf8.Valid(data) {
		return indexedSource(string(data), "UTF-8", encodingSourceUTF8, false), nil
	}
	if len(fallbacks) == 0 {
		return skippedSource("", "", skipReasonEncodingUnknown), nil
	}
	for _, fallback := range fallbacks {
		text, err := decodeNamed(data, fallback)
		if errors.Is(err, errTranscoderUnavailable) {
			return skippedSource(fallback, encodingSourceFallback, skipReasonTranscoderUnavailable), nil
		}
		if err == nil {
			return indexedSource(text, canonicalUnicodeEncoding(fallback), encodingSourceFallback, !isUTF8Encoding(fallback)), nil
		}
	}
	return skippedSource("", encodingSourceFallback, skipReasonConversionFailed), nil
}

func indexedSource(text, encoding, source string, transcoded bool) decodedSource {
	return decodedSource{text: text, indexStatus: indexStatusIndexed, sourceEncoding: encoding, encodingSource: source, transcoded: transcoded}
}

func skippedSource(encoding, source, reason string) decodedSource {
	return decodedSource{indexStatus: indexStatusSkipped, sourceEncoding: encoding, encodingSource: source, skipReason: reason}
}

func unicodeBOM(data []byte) (string, int) {
	for _, candidate := range []struct {
		bom      []byte
		encoding string
	}{
		{[]byte{0x00, 0x00, 0xfe, 0xff}, "UTF-32BE"},
		{[]byte{0xff, 0xfe, 0x00, 0x00}, "UTF-32LE"},
		{[]byte{0xef, 0xbb, 0xbf}, "UTF-8"},
		{[]byte{0xfe, 0xff}, "UTF-16BE"},
		{[]byte{0xff, 0xfe}, "UTF-16LE"},
	} {
		if bytes.HasPrefix(data, candidate.bom) {
			return candidate.encoding, len(candidate.bom)
		}
	}
	return "", 0
}

func decodeNamed(data []byte, encoding string) (string, error) {
	switch normalizedEncoding(encoding) {
	case "utf8":
		if !utf8.Valid(data) {
			return "", errors.New("invalid UTF-8")
		}
		return strings.TrimPrefix(string(data), "\ufeff"), nil
	case "utf16le":
		return decodeUTF16(data, binary.LittleEndian)
	case "utf16be":
		return decodeUTF16(data, binary.BigEndian)
	case "utf32le":
		return decodeUTF32(data, binary.LittleEndian)
	case "utf32be":
		return decodeUTF32(data, binary.BigEndian)
	default:
		return iconvTranscode(data, encoding)
	}
}

func runIconv(data []byte, encoding string) (string, error) {
	path, err := exec.LookPath("iconv")
	if err != nil {
		return "", errTranscoderUnavailable
	}
	cmd := exec.Command(path, "-f", encoding, "-t", "UTF-8")
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	if !utf8.Valid(out) {
		return "", errors.New("iconv produced invalid UTF-8")
	}
	return strings.TrimPrefix(string(out), "\ufeff"), nil
}

func decodeUTF16(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%2 != 0 {
		return "", errors.New("odd UTF-16 byte length")
	}
	units := make([]uint16, len(data)/2)
	for i := range units {
		units[i] = order.Uint16(data[i*2:])
	}
	for i := 0; i < len(units); i++ {
		if 0xd800 <= units[i] && units[i] <= 0xdbff {
			if i+1 >= len(units) || units[i+1] < 0xdc00 || units[i+1] > 0xdfff {
				return "", errors.New("invalid UTF-16 surrogate pair")
			}
			i++
		} else if 0xdc00 <= units[i] && units[i] <= 0xdfff {
			return "", errors.New("invalid UTF-16 surrogate")
		}
	}
	return string(utf16.Decode(units)), nil
}

func decodeUTF32(data []byte, order binary.ByteOrder) (string, error) {
	if len(data)%4 != 0 {
		return "", errors.New("invalid UTF-32 byte length")
	}
	runes := make([]rune, 0, len(data)/4)
	for offset := 0; offset < len(data); offset += 4 {
		value := order.Uint32(data[offset:])
		if value > utf8.MaxRune || 0xd800 <= value && value <= 0xdfff {
			return "", errors.New("invalid UTF-32 code point")
		}
		runes = append(runes, rune(value))
	}
	return string(runes), nil
}

func declaredEncoding(data []byte, language string) string {
	lines := firstByteLines(data, 2)
	switch language {
	case "ruby":
		if len(lines) == 0 {
			return ""
		}
		line := lines[0]
		if bytes.HasPrefix(line, []byte("#!")) {
			if len(lines) < 2 {
				return ""
			}
			line = lines[1]
		}
		return encodingMatch(line, rubyEncodingPattern)
	case "python":
		for i, line := range lines {
			trimmed := bytes.TrimSpace(line)
			if i == 1 && (len(lines) < 2 || !bytes.HasPrefix(bytes.TrimSpace(lines[0]), []byte("#"))) {
				return ""
			}
			if bytes.HasPrefix(trimmed, []byte("#")) {
				if value := encodingMatch(trimmed, pythonEncodingPattern); value != "" {
					return value
				}
			}
		}
	}
	return ""
}

func firstByteLines(data []byte, limit int) [][]byte {
	data = bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
	data = bytes.ReplaceAll(data, []byte("\r"), []byte("\n"))
	lines := bytes.Split(data, []byte("\n"))
	if len(lines) > limit {
		lines = lines[:limit]
	}
	return lines
}

func encodingMatch(line []byte, pattern *regexp.Regexp) string {
	match := pattern.FindSubmatch(line)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func normalizedEncoding(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "")
	value = strings.ReplaceAll(value, "_", "")
	return value
}

func isUTF8Encoding(value string) bool {
	return normalizedEncoding(value) == "utf8"
}

func canonicalUnicodeEncoding(value string) string {
	switch normalizedEncoding(value) {
	case "utf8":
		return "UTF-8"
	case "utf16le":
		return "UTF-16LE"
	case "utf16be":
		return "UTF-16BE"
	case "utf32le":
		return "UTF-32LE"
	case "utf32be":
		return "UTF-32BE"
	default:
		return strings.TrimSpace(value)
	}
}

func equivalentEncoding(left, right string) bool {
	left = normalizedEncoding(left)
	right = normalizedEncoding(right)
	if left == right {
		return true
	}
	return left == "utf16" && strings.HasPrefix(right, "utf16") ||
		right == "utf16" && strings.HasPrefix(left, "utf16") ||
		left == "utf32" && strings.HasPrefix(right, "utf32") ||
		right == "utf32" && strings.HasPrefix(left, "utf32")
}
