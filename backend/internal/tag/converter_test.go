package tag

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestConvertBinaryDecodesSupportedDataTypes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		raw        []byte
		conversion BinaryConversion
		want       any
	}{
		{name: "bool false", raw: []byte{0b00000010}, conversion: BinaryConversion{DataType: DataTypeBool}, want: false},
		{name: "bool selected bit", raw: []byte{0b00100000}, conversion: BinaryConversion{DataType: DataTypeBool, BitOffset: 5}, want: true},
		{name: "int16", raw: []byte{0xFF, 0xFE}, conversion: BinaryConversion{DataType: DataTypeInt16}, want: int16(-2)},
		{name: "uint16 little endian", raw: []byte{0x34, 0x12}, conversion: BinaryConversion{DataType: DataTypeUInt16, ByteOrder: ByteOrderLittleEndian}, want: uint16(0x1234)},
		{name: "int32", raw: []byte{0xFF, 0xFF, 0xFF, 0xFE}, conversion: BinaryConversion{DataType: DataTypeInt32}, want: int32(-2)},
		{name: "uint32 word swap", raw: []byte{0x03, 0x04, 0x01, 0x02}, conversion: BinaryConversion{DataType: DataTypeUInt32, ByteOrder: ByteOrderWordSwap}, want: uint32(0x01020304)},
		{name: "float32 byte swap", raw: []byte{0x48, 0x41, 0x00, 0x00}, conversion: BinaryConversion{DataType: DataTypeFloat32, ByteOrder: ByteOrderByteSwap}, want: float32(12.5)},
		{name: "float64 offset", raw: append([]byte{0xAA, 0xBB}, float64Bytes(-42.25)...), conversion: BinaryConversion{DataType: DataTypeFloat64, ByteOffset: 2}, want: -42.25},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ConvertBinary(test.raw, test.conversion)
			if err != nil {
				t.Fatalf("ConvertBinary() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ConvertBinary() = %#v (%T), want %#v (%T)", got, got, test.want, test.want)
			}
		})
	}
}

func TestConvertBinaryDecodesIndustrialFloat32ByteOrders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		raw       []byte
		byteOrder ByteOrder
		want      float32
	}{
		{name: "ABCD big endian", raw: []byte{0x41, 0x32, 0x14, 0x7B}, byteOrder: ByteOrderBigEndian, want: 11.13},
		{name: "CDAB word swap", raw: []byte{0x0D, 0x05, 0x42, 0x05}, byteOrder: ByteOrderWordSwap, want: 33.262714},
		{name: "BADC byte swap", raw: []byte{0x32, 0x42, 0x46, 0xB6}, byteOrder: ByteOrderByteSwap, want: 44.678},
		{name: "DCBA little endian", raw: []byte{0x5A, 0xE4, 0x5C, 0x42}, byteOrder: ByteOrderLittleEndian, want: 55.223},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := ConvertBinary(test.raw, BinaryConversion{DataType: DataTypeFloat32, ByteOrder: test.byteOrder})
			if err != nil {
				t.Fatalf("ConvertBinary() error = %v", err)
			}
			if got != test.want {
				t.Errorf("ConvertBinary() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestApplyByteOrderProducesCanonicalBigEndianWithoutMutatingInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		order ByteOrder
		raw   []byte
		want  []byte
	}{
		{name: "default", raw: []byte{1, 2, 3, 4}, want: []byte{1, 2, 3, 4}},
		{name: "big endian", order: ByteOrderBigEndian, raw: []byte{1, 2, 3, 4}, want: []byte{1, 2, 3, 4}},
		{name: "little endian", order: ByteOrderLittleEndian, raw: []byte{4, 3, 2, 1}, want: []byte{1, 2, 3, 4}},
		{name: "word swap", order: ByteOrderWordSwap, raw: []byte{3, 4, 1, 2}, want: []byte{1, 2, 3, 4}},
		{name: "byte swap", order: ByteOrderByteSwap, raw: []byte{2, 1, 4, 3}, want: []byte{1, 2, 3, 4}},
		{name: "four word swap", order: ByteOrderWordSwap, raw: []byte{7, 8, 5, 6, 3, 4, 1, 2}, want: []byte{1, 2, 3, 4, 5, 6, 7, 8}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			original := append([]byte(nil), test.raw...)
			got, err := ApplyByteOrder(test.raw, test.order)
			if err != nil {
				t.Fatalf("ApplyByteOrder() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Errorf("ApplyByteOrder() = %v, want %v", got, test.want)
			}
			if !reflect.DeepEqual(test.raw, original) {
				t.Errorf("ApplyByteOrder() mutated input to %v", test.raw)
			}
		})
	}
}

func TestConvertBinaryRejectsInvalidRequests(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		raw        []byte
		conversion BinaryConversion
		want       error
	}{
		{name: "unsupported data type", raw: []byte{0, 1}, conversion: BinaryConversion{DataType: "string"}, want: ErrInvalidBinaryConversion},
		{name: "negative byte offset", raw: []byte{0, 1}, conversion: BinaryConversion{DataType: DataTypeUInt16, ByteOffset: -1}, want: ErrInvalidBinaryConversion},
		{name: "offset beyond payload", raw: []byte{0, 1}, conversion: BinaryConversion{DataType: DataTypeUInt16, ByteOffset: 3}, want: ErrInsufficientBinaryData},
		{name: "short payload", raw: []byte{0, 1, 2}, conversion: BinaryConversion{DataType: DataTypeUInt32}, want: ErrInsufficientBinaryData},
		{name: "bit offset over seven", raw: []byte{0}, conversion: BinaryConversion{DataType: DataTypeBool, BitOffset: 8}, want: ErrInvalidBinaryConversion},
		{name: "bit offset for number", raw: []byte{0, 1}, conversion: BinaryConversion{DataType: DataTypeUInt16, BitOffset: 1}, want: ErrInvalidBinaryConversion},
		{name: "unknown byte order", raw: []byte{0, 1}, conversion: BinaryConversion{DataType: DataTypeUInt16, ByteOrder: "middle_endian"}, want: ErrInvalidBinaryConversion},
		{name: "non-finite float", raw: []byte{0x7F, 0xC0, 0x00, 0x00}, conversion: BinaryConversion{DataType: DataTypeFloat32}, want: ErrNonFiniteBinaryValue},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ConvertBinary(test.raw, test.conversion); !errors.Is(err, test.want) {
				t.Fatalf("ConvertBinary() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestApplyByteOrderRejectsOddPayload(t *testing.T) {
	t.Parallel()
	if _, err := ApplyByteOrder([]byte{1, 2, 3}, ByteOrderBigEndian); !errors.Is(err, ErrInvalidBinaryConversion) {
		t.Fatalf("ApplyByteOrder() error = %v, want invalid conversion", err)
	}
}

func float64Bytes(value float64) []byte {
	bits := math.Float64bits(value)
	return []byte{byte(bits >> 56), byte(bits >> 48), byte(bits >> 40), byte(bits >> 32), byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits)}
}
