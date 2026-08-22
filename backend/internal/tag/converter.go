package tag

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

var (
	ErrInvalidBinaryConversion = errors.New("invalid binary conversion")
	ErrInsufficientBinaryData  = errors.New("insufficient binary data")
	ErrNonFiniteBinaryValue    = errors.New("binary value is not finite")
)

type ByteOrder string

const (
	ByteOrderBigEndian    ByteOrder = "big_endian"
	ByteOrderLittleEndian ByteOrder = "little_endian"
	ByteOrderWordSwap     ByteOrder = "word_swap"
	ByteOrderByteSwap     ByteOrder = "byte_swap"
)

type BinaryConversion struct {
	DataType   DataType
	ByteOrder  ByteOrder
	ByteOffset int
	BitOffset  uint8
}

func ConvertBinary(raw []byte, conversion BinaryConversion) (any, error) {
	if conversion.ByteOffset < 0 {
		return nil, fmt.Errorf("%w: byte_offset must not be negative", ErrInvalidBinaryConversion)
	}
	size, err := binaryDataSize(conversion.DataType)
	if err != nil {
		return nil, err
	}
	if conversion.ByteOffset > len(raw) || size > len(raw)-conversion.ByteOffset {
		return nil, fmt.Errorf("%w: need %d bytes at offset %d, have %d", ErrInsufficientBinaryData, size, conversion.ByteOffset, len(raw))
	}
	if conversion.DataType != DataTypeBool && conversion.BitOffset != 0 {
		return nil, fmt.Errorf("%w: bit_offset is only valid for bool", ErrInvalidBinaryConversion)
	}
	window := raw[conversion.ByteOffset : conversion.ByteOffset+size]
	if conversion.DataType == DataTypeBool {
		if conversion.BitOffset > 7 {
			return nil, fmt.Errorf("%w: bit_offset must be between 0 and 7", ErrInvalidBinaryConversion)
		}
		return window[0]&(1<<conversion.BitOffset) != 0, nil
	}

	ordered, err := ApplyByteOrder(window, conversion.ByteOrder)
	if err != nil {
		return nil, err
	}
	switch conversion.DataType {
	case DataTypeInt16:
		return int16(binary.BigEndian.Uint16(ordered)), nil
	case DataTypeUInt16:
		return binary.BigEndian.Uint16(ordered), nil
	case DataTypeInt32:
		return int32(binary.BigEndian.Uint32(ordered)), nil
	case DataTypeUInt32:
		return binary.BigEndian.Uint32(ordered), nil
	case DataTypeFloat32:
		value := math.Float32frombits(binary.BigEndian.Uint32(ordered))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, ErrNonFiniteBinaryValue
		}
		return value, nil
	case DataTypeFloat64:
		value := math.Float64frombits(binary.BigEndian.Uint64(ordered))
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, ErrNonFiniteBinaryValue
		}
		return value, nil
	default:
		return nil, fmt.Errorf("%w: unsupported data type %q", ErrInvalidBinaryConversion, conversion.DataType)
	}
}

func ApplyByteOrder(raw []byte, order ByteOrder) ([]byte, error) {
	if len(raw)%2 != 0 {
		return nil, fmt.Errorf("%w: byte order requires an even byte count", ErrInvalidBinaryConversion)
	}
	if order == "" {
		order = ByteOrderBigEndian
	}
	ordered := append([]byte(nil), raw...)
	switch order {
	case ByteOrderBigEndian:
		return ordered, nil
	case ByteOrderLittleEndian:
		reverseBytes(ordered)
		return ordered, nil
	case ByteOrderWordSwap:
		for left, right := 0, len(ordered)-2; left < right; left, right = left+2, right-2 {
			ordered[left], ordered[right] = ordered[right], ordered[left]
			ordered[left+1], ordered[right+1] = ordered[right+1], ordered[left+1]
		}
		return ordered, nil
	case ByteOrderByteSwap:
		for index := 0; index < len(ordered); index += 2 {
			ordered[index], ordered[index+1] = ordered[index+1], ordered[index]
		}
		return ordered, nil
	default:
		return nil, fmt.Errorf("%w: unsupported byte order %q", ErrInvalidBinaryConversion, order)
	}
}

func binaryDataSize(dataType DataType) (int, error) {
	switch dataType {
	case DataTypeBool:
		return 1, nil
	case DataTypeInt16, DataTypeUInt16:
		return 2, nil
	case DataTypeInt32, DataTypeUInt32, DataTypeFloat32:
		return 4, nil
	case DataTypeFloat64:
		return 8, nil
	default:
		return 0, fmt.Errorf("%w: unsupported data type %q", ErrInvalidBinaryConversion, dataType)
	}
}

func reverseBytes(value []byte) {
	for left, right := 0, len(value)-1; left < right; left, right = left+1, right-1 {
		value[left], value[right] = value[right], value[left]
	}
}
