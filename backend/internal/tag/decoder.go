package tag

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"github.com/thefuriousowl/iot-edge/internal/protocol"
)

const BinaryNumericDecoderType = "binary_numeric"

var (
	ErrInvalidDecoderConfig = errors.New("invalid reading decoder config")
	ErrUnsupportedDecoder   = errors.New("unsupported reading decoder")
)

type DatasourceReader interface {
	ReadDatasourceForTag(context.Context, uuid.UUID) (protocol.DatasourceSample, error)
}

type ReadingDecoder interface {
	Type() string
	NormalizeConfig(DataType, Config) (Config, error)
	Decode(protocol.DatasourceSample, DataType, Config) (any, error)
}

type binaryNumericDecoder struct{}

type binaryNumericConfigInput struct {
	ByteOffset *int       `json:"byte_offset"`
	ByteOrder  *ByteOrder `json:"byte_order"`
	BitOffset  *uint8     `json:"bit_offset"`
}

type binaryNumericConfig struct {
	ByteOffset int       `json:"byte_offset"`
	ByteOrder  ByteOrder `json:"byte_order"`
	BitOffset  uint8     `json:"bit_offset"`
}

func NewBinaryNumericDecoder() ReadingDecoder { return &binaryNumericDecoder{} }

func (*binaryNumericDecoder) Type() string { return BinaryNumericDecoderType }

func (*binaryNumericDecoder) NormalizeConfig(dataType DataType, raw Config) (Config, error) {
	if _, err := binaryDataSize(dataType); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDecoderConfig, err)
	}
	var input binaryNumericConfigInput
	if len(raw) == 0 {
		raw = json.RawMessage(`{}`)
	}
	if err := decodeStrictJSON(raw, &input); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDecoderConfig, err)
	}
	config := binaryNumericConfig{ByteOrder: ByteOrderBigEndian}
	if input.ByteOffset != nil {
		config.ByteOffset = *input.ByteOffset
	}
	if input.ByteOrder != nil {
		config.ByteOrder = *input.ByteOrder
	}
	if input.BitOffset != nil {
		config.BitOffset = *input.BitOffset
	}
	if config.ByteOffset < 0 {
		return nil, fmt.Errorf("%w: byte_offset must not be negative", ErrInvalidDecoderConfig)
	}
	if !validByteOrder(config.ByteOrder) {
		return nil, fmt.Errorf("%w: unsupported byte_order %q", ErrInvalidDecoderConfig, config.ByteOrder)
	}
	if dataType == DataTypeBool {
		if config.BitOffset > 7 {
			return nil, fmt.Errorf("%w: bit_offset must be between 0 and 7", ErrInvalidDecoderConfig)
		}
	} else if config.BitOffset != 0 {
		return nil, fmt.Errorf("%w: bit_offset is only valid for bool", ErrInvalidDecoderConfig)
	}
	canonical, err := json.Marshal(config)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDecoderConfig, err)
	}
	return canonical, nil
}

func (decoder *binaryNumericDecoder) Decode(sample protocol.DatasourceSample, dataType DataType, raw Config) (any, error) {
	canonical, err := decoder.NormalizeConfig(dataType, raw)
	if err != nil {
		return nil, err
	}
	var config binaryNumericConfig
	if err := json.Unmarshal(canonical, &config); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDecoderConfig, err)
	}
	return ConvertBinary(sample.Raw, BinaryConversion{DataType: dataType, ByteOffset: config.ByteOffset, ByteOrder: config.ByteOrder, BitOffset: config.BitOffset})
}

func validByteOrder(order ByteOrder) bool {
	switch order {
	case ByteOrderBigEndian, ByteOrderLittleEndian, ByteOrderWordSwap, ByteOrderByteSwap:
		return true
	default:
		return false
	}
}

func decodeStrictJSON(raw json.RawMessage, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON documents")
		}
		return err
	}
	return nil
}
