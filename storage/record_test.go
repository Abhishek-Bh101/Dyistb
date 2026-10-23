package storage

import (
	"bytes"
	"testing"
)

// TestRecordRoundTrip checks whether encoding a record and then decoding it gives back the original record without losing or changing any data
func TestRecordRoundTrip(t *testing.T) {
	tests := []struct {
		name string
		rec  record
	}{
		{
			name: "put",
			rec: record{
				timestamp: 1_800_000_000_123,
				typ:       recordTypePut,
				key:       []byte("name"),
				value:     []byte("Abhishek"),
			},
		},
		{
			name: "put with empty value",
			rec: record{
				timestamp: 1_800_000_000_456,
				typ:       recordTypePut,
				key:       []byte("empty"),
				value:     []byte{},
			},
		},
		{
			name: "binary key and value",
			rec: record{
				timestamp: 1_800_000_000_789,
				typ:       recordTypePut,
				key:       []byte{0x00, 0xff, 0x10},
				value:     []byte{0xff, 0x00, 0x7f, 0x80},
			},
		},
		{
			name: "delete",
			rec: record{
				timestamp: 1_800_000_001_000,
				typ:       recordTypeDelete,
				key:       []byte("name"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := encodeRecord(tt.rec)
			if err != nil {
				t.Fatalf("encodeRecord() error: %v", err)
			}

			reader := bytes.NewReader(encoded)

			got, err := decodeRecord(reader, int64(len(encoded)))
			if err != nil {
				t.Fatalf("decodeRecord() error: %v", err)
			}

			if got.timestamp != tt.rec.timestamp {
				t.Errorf(
					"timestamp = %d, want %d",
					got.timestamp,
					tt.rec.timestamp,
				)
			}

			if got.typ != tt.rec.typ {
				t.Errorf("type = %d, want %d", got.typ, tt.rec.typ)
			}

			if !bytes.Equal(got.key, tt.rec.key) {
				t.Errorf("key = %v, want %v", got.key, tt.rec.key)
			}

			if !bytes.Equal(got.value, tt.rec.value) {
				t.Errorf("value = %v, want %v", got.value, tt.rec.value)
			}

			if gotSize := got.encodedSize(); gotSize != int64(len(encoded)) {
				t.Errorf(
					"encodedSize() = %d, want %d",
					gotSize,
					len(encoded),
				)
			}

			if reader.Len() != 0 {
				t.Errorf("%d unexpected bytes remain", reader.Len())
			}
		})
	}
}
