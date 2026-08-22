export type TagType = "reading" | "constant" | "calculated";

export type TagDataType =
  | "bool"
  | "int16"
  | "uint16"
  | "int32"
  | "uint32"
  | "float32"
  | "float64";

export type TagByteOrder =
  | "big_endian"
  | "little_endian"
  | "word_swap"
  | "byte_swap";

export type TagValue = boolean | number;

export interface BinaryNumericDecoderConfig {
  byte_offset: number;
  byte_order: TagByteOrder;
  bit_offset: number;
}

export interface BinaryNumericDecoderConfigInput {
  byte_offset?: number;
  byte_order?: TagByteOrder;
  bit_offset?: number;
}

export interface ReadingDecoderConfigByType {
  binary_numeric: BinaryNumericDecoderConfig;
}

export interface ReadingDecoderInputConfigByType {
  binary_numeric: BinaryNumericDecoderConfigInput;
}

export type ReadingDecoder = {
  [DecoderType in keyof ReadingDecoderConfigByType]: {
    type: DecoderType;
    config: ReadingDecoderConfigByType[DecoderType];
  };
}[keyof ReadingDecoderConfigByType];

export type ReadingDecoderInput = {
  [DecoderType in keyof ReadingDecoderInputConfigByType]: {
    type: DecoderType;
    config?: ReadingDecoderInputConfigByType[DecoderType];
  };
}[keyof ReadingDecoderInputConfigByType];

export interface ReadingTagConfig {
  decoder: ReadingDecoder;
}

export interface ReadingTagConfigInput {
  decoder: ReadingDecoderInput;
}

export interface ConstantTagConfig {
  value: TagValue;
}

export interface CalculatedTagConfig {
  expression: string;
}

interface TagBase {
  id: string;
  name: string;
  data_type: TagDataType;
  description: string | null;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface ReadingTag extends TagBase {
  type: "reading";
  datasource_id: string;
  config: ReadingTagConfig;
}

export interface ConstantTag extends TagBase {
  type: "constant";
  datasource_id: null;
  config: ConstantTagConfig;
}

export interface CalculatedTag extends TagBase {
  type: "calculated";
  datasource_id: null;
  config: CalculatedTagConfig;
}

export type Tag = ReadingTag | ConstantTag | CalculatedTag;

interface CreateTagRequestBase {
  name: string;
  data_type: TagDataType;
  description?: string | null;
  enabled?: boolean;
}

export interface CreateReadingTagRequest extends CreateTagRequestBase {
  type: "reading";
  datasource_id: string;
  config: ReadingTagConfigInput;
}

export interface CreateConstantTagRequest extends CreateTagRequestBase {
  type: "constant";
  config: ConstantTagConfig;
}

export interface CreateCalculatedTagRequest extends CreateTagRequestBase {
  type: "calculated";
  config: CalculatedTagConfig;
}

export type CreateTagRequest =
  | CreateReadingTagRequest
  | CreateConstantTagRequest
  | CreateCalculatedTagRequest;

export interface UpdateTagRequest {
  datasource_id?: string | null;
  name?: string;
  data_type?: TagDataType;
  description?: string | null;
  enabled?: boolean;
  config?: ReadingTagConfigInput | ConstantTagConfig | CalculatedTagConfig;
}

export interface TagListParams {
  type?: TagType;
  data_type?: TagDataType;
  enabled?: boolean;
  datasource_id?: string;
  search?: string;
  page?: number;
  per_page?: number;
}

export interface TagPagination {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

export interface TagListResponse {
  data: Tag[];
  pagination: TagPagination;
}

export type PreviewTagRequest =
  | Pick<CreateReadingTagRequest, "type" | "datasource_id" | "data_type" | "config">
  | Pick<CreateConstantTagRequest, "type" | "data_type" | "config">
  | Pick<CreateCalculatedTagRequest, "type" | "data_type" | "config">;

export interface TagPreviewResult {
  tag_id: string;
  observed_at: string;
  quality: "good" | "bad" | "uncertain";
  data_type: TagDataType;
  value: TagValue;
}

export interface ValidateTagExpressionRequest {
  tag_id?: string;
  expression: string;
}

export interface ValidateTagExpressionResponse {
  valid: true;
  dependencies: string[];
}

export type TagErrorCode =
  | "TAG001"
  | "TAG004"
  | "TAG006"
  | "TAG007"
  | "DS008"
  | "VALIDATION_ERROR"
  | "INTERNAL_ERROR";

export interface TagErrorResponse {
  error: {
    code: TagErrorCode;
    message: string;
  };
}
