import { describe, expect, expectTypeOf, it } from "vitest";

import type {
  CreateTagRequest,
  ReadingDecoderConfigByType,
  Tag,
  TagDataType,
  TagErrorCode,
  TagListResponse,
  TagPreviewResult,
  UpdateTagRequest,
  ValidateTagExpressionResponse,
} from "./tag";

const readingTag: Tag = {
  id: "tag-reading",
  datasource_id: "source-1",
  name: "Line voltage",
  type: "reading",
  data_type: "float32",
  description: null,
  enabled: true,
  config: {
    decoder: {
      type: "binary_numeric",
      config: {
        byte_offset: 2,
        byte_order: "word_swap",
        bit_offset: 0,
      },
    },
  },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

describe("Tag types", () => {
  it("narrows response config by tag and decoder type", () => {
    expect(readingTag.type).toBe("reading");
    if (readingTag.type === "reading") {
      expect(readingTag.datasource_id).toBe("source-1");
      expect(readingTag.config.decoder.config.byte_order).toBe("word_swap");
    }
    expectTypeOf<ReadingDecoderConfigByType>().toHaveProperty(
      "binary_numeric",
    );
  });

  it("models each create request as a discriminated union", () => {
    const requests = [
      {
        name: "Input",
        type: "reading",
        data_type: "uint16",
        datasource_id: "source-1",
        config: {
          decoder: {
            type: "binary_numeric",
            config: { byte_order: "little_endian" },
          },
        },
      },
      {
        name: "Nominal",
        type: "constant",
        data_type: "float64",
        config: { value: 230.5 },
      },
      {
        name: "Delta",
        type: "calculated",
        data_type: "float64",
        config: { expression: "${tag-reading} - 230" },
      },
    ] satisfies CreateTagRequest[];

    expect(requests.map((request) => request.type)).toEqual([
      "reading",
      "constant",
      "calculated",
    ]);
    expectTypeOf<TagDataType>().toEqualTypeOf<
      | "bool"
      | "int16"
      | "uint16"
      | "int32"
      | "uint32"
      | "float32"
      | "float64"
    >();
  });

  it("keeps tag type immutable while allowing nullable ownership updates", () => {
    const update: UpdateTagRequest = {
      datasource_id: null,
      description: null,
      enabled: false,
    };

    expect(update.datasource_id).toBeNull();
    expectTypeOf<UpdateTagRequest>().not.toHaveProperty("type");
  });

  it("models pagination, previews, validation, and API errors", () => {
    expectTypeOf<TagListResponse["data"]>().toBeArray();
    expectTypeOf<TagPreviewResult["value"]>().toEqualTypeOf<
      boolean | number
    >();
    expectTypeOf<ValidateTagExpressionResponse["dependencies"]>().toBeArray();
    const code: TagErrorCode = "TAG007";

    expect(code).toBe("TAG007");
  });
});
