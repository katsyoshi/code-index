package main

import (
	"encoding/binary"
	"errors"
	"os/exec"
	"reflect"
	"testing"
)

func TestDecodeSourceUnicodeBOMs(t *testing.T) {
	tests := []struct {
		name, text, encoding string
		data                 []byte
		transcoded           bool
	}{
		{"utf8", "hello\n", "UTF-8", append([]byte{0xef, 0xbb, 0xbf}, []byte("hello\n")...), false},
		{"utf16le", "hi\n", "UTF-16LE", encodedUnits([]byte{0xff, 0xfe}, binary.LittleEndian, []uint16{'h', 'i', '\n'}), true},
		{"utf16be", "hi\n", "UTF-16BE", encodedUnits([]byte{0xfe, 0xff}, binary.BigEndian, []uint16{'h', 'i', '\n'}), true},
		{"utf32le", "hi\n", "UTF-32LE", encodedWords([]byte{0xff, 0xfe, 0, 0}, binary.LittleEndian, []uint32{'h', 'i', '\n'}), true},
		{"utf32be", "hi\n", "UTF-32BE", encodedWords([]byte{0, 0, 0xfe, 0xff}, binary.BigEndian, []uint32{'h', 'i', '\n'}), true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decodeSource(test.data, "", nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.indexStatus != indexStatusIndexed || got.text != test.text || got.sourceEncoding != test.encoding || got.encodingSource != encodingSourceBOM || got.transcoded != test.transcoded {
				t.Fatalf("decodeSource() = %#v", got)
			}
		})
	}
}

func TestDecodeSourceRejectsMalformedUnicodeAndNUL(t *testing.T) {
	tests := [][]byte{
		{0xff, 0xfe, 0x00},
		encodedUnits([]byte{0xff, 0xfe}, binary.LittleEndian, []uint16{0xd800}),
		encodedWords([]byte{0xff, 0xfe, 0, 0}, binary.LittleEndian, []uint32{0x110000}),
	}
	for _, data := range tests {
		got, err := decodeSource(data, "", nil)
		if err != nil || got.indexStatus != indexStatusSkipped || got.skipReason != skipReasonConversionFailed {
			t.Fatalf("decodeSource(%x) = %#v, %v", data, got, err)
		}
	}
	if _, err := decodeSource([]byte{'a', 0, 'b'}, "", nil); !errors.Is(err, errNotText) {
		t.Fatalf("NUL decode error = %v", err)
	}
}

func TestDecodeSourceDeclarationsAndFallbacks(t *testing.T) {
	original := iconvTranscode
	t.Cleanup(func() { iconvTranscode = original })
	var encodings []string
	iconvTranscode = func(_ []byte, encoding string) (string, error) {
		encodings = append(encodings, encoding)
		if encoding == "broken" {
			return "", errors.New("bad encoding")
		}
		return "decoded", nil
	}

	ruby, err := decodeSource([]byte("#!/usr/bin/env ruby\n# encoding: Windows-31J\nputs :ok\n"), "ruby", []string{"fallback"})
	if err != nil || ruby.text != "decoded" || ruby.encodingSource != encodingSourceDeclaration || ruby.sourceEncoding != "Windows-31J" {
		t.Fatalf("Ruby declaration = %#v, %v", ruby, err)
	}
	python, err := decodeSource([]byte("# comment\n# coding=EUC-JP\nprint('ok')\n"), "python", nil)
	if err != nil || python.encodingSource != encodingSourceDeclaration || python.sourceEncoding != "EUC-JP" {
		t.Fatalf("Python declaration = %#v, %v", python, err)
	}
	fallback, err := decodeSource([]byte{0xff, 0x61}, "", []string{"broken", "working"})
	if err != nil || fallback.text != "decoded" || fallback.encodingSource != encodingSourceFallback || fallback.sourceEncoding != "working" {
		t.Fatalf("fallback = %#v, %v", fallback, err)
	}
	if !reflect.DeepEqual(encodings, []string{"Windows-31J", "EUC-JP", "broken", "working"}) {
		t.Fatalf("iconv encodings = %#v", encodings)
	}
}

func TestDecodeSourceConflictAndFailures(t *testing.T) {
	data := append([]byte{0xef, 0xbb, 0xbf}, []byte("# encoding: Windows-31J\n")...)
	conflict, err := decodeSource(data, "ruby", nil)
	if err != nil || conflict.skipReason != skipReasonEncodingConflict {
		t.Fatalf("conflict = %#v, %v", conflict, err)
	}

	original := iconvTranscode
	t.Cleanup(func() { iconvTranscode = original })
	iconvTranscode = func([]byte, string) (string, error) { return "", errTranscoderUnavailable }
	unavailable, err := decodeSource([]byte{0xff}, "", []string{"Windows-31J"})
	if err != nil || unavailable.skipReason != skipReasonTranscoderUnavailable {
		t.Fatalf("unavailable = %#v, %v", unavailable, err)
	}
	unknown, err := decodeSource([]byte{0xff}, "", nil)
	if err != nil || unknown.skipReason != skipReasonEncodingUnknown {
		t.Fatalf("unknown = %#v, %v", unknown, err)
	}
}

func TestValidateEncodingFallbacks(t *testing.T) {
	got, err := validateEncodingFallbacks([]string{" Windows-31J ", "EUC-JP"})
	if err != nil || !reflect.DeepEqual(got, []string{"Windows-31J", "EUC-JP"}) {
		t.Fatalf("fallbacks = %#v, %v", got, err)
	}
	for _, values := range [][]string{{""}, {"EUC-JP", "euc-jp"}} {
		if _, err := validateEncodingFallbacks(values); err == nil {
			t.Fatalf("fallbacks %#v succeeded", values)
		}
	}
}

func TestRunIconv(t *testing.T) {
	if _, err := exec.LookPath("iconv"); err != nil {
		t.Skip("iconv command not found")
	}
	got, err := runIconv([]byte{0x82, 0xa0}, "SHIFT_JIS")
	if err != nil {
		t.Fatal(err)
	}
	if got != "あ" {
		t.Fatalf("runIconv() = %q", got)
	}
}

func encodedUnits(prefix []byte, order binary.ByteOrder, values []uint16) []byte {
	result := append([]byte{}, prefix...)
	for _, value := range values {
		var encoded [2]byte
		order.PutUint16(encoded[:], value)
		result = append(result, encoded[:]...)
	}
	return result
}

func encodedWords(prefix []byte, order binary.ByteOrder, values []uint32) []byte {
	result := append([]byte{}, prefix...)
	for _, value := range values {
		var encoded [4]byte
		order.PutUint32(encoded[:], value)
		result = append(result, encoded[:]...)
	}
	return result
}
