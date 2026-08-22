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

export interface LinearTransformConfig {
  gain: number;
  offset: number;
}

export interface LinearTransformConfigInput {
  gain: number;
  offset?: number;
}

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
    data_type?: TagDataType;
    config: ReadingDecoderConfigByType[DecoderType];
  };
}[keyof ReadingDecoderConfigByType];

export type ReadingDecoderInput = {
  [DecoderType in keyof ReadingDecoderInputConfigByType]: {
    type: DecoderType;
    data_type?: TagDataType;
    config?: ReadingDecoderInputConfigByType[DecoderType];
  };
}[keyof ReadingDecoderInputConfigByType];

export interface ReadingTagConfig {
  decoder: ReadingDecoder;
  transform?: {
    type: "linear";
    config: LinearTransformConfig;
  };
}

export interface ReadingTagConfigInput {
  decoder: ReadingDecoderInput;
  transform?: {
    type: "linear";
    config: LinearTransformConfigInput;
  };
}

export interface ConstantTagConfig {
  value: TagValue;
}

export interface CalculatedTagConfig {
  expression: string;
  trigger?: {
    tag_id: string;
    mode?: "on_sample";
  };
}

export interface CalculatedTagConfigInput {
  expression: string;
  trigger: {
    tag_id: string;
    mode?: "on_sample";
  };
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
  config: CalculatedTagConfigInput;
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
  config?: ReadingTagConfigInput | ConstantTagConfig | CalculatedTagConfigInput;
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

export interface TagRuntimeValue {
  tag_id: string;
  sequence: number;
  observed_at: string;
  stored_at: string;
  quality: "good" | "bad";
  data_type: TagDataType;
  value: TagValue | null;
  error?: string;
}

export interface TagValuesResponse {
  latest: TagRuntimeValue | null;
  history: TagRuntimeValue[];
  latest_retention: "persistent";
  history_retention: "runtime_memory";
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
  | "TAG008"
  | "TAG009"
  | "DS008"
  | "VALIDATION_ERROR"
  | "INTERNAL_ERROR";

export interface TagErrorResponse {
  error: {
    code: TagErrorCode;
    message: string;
  };
}
