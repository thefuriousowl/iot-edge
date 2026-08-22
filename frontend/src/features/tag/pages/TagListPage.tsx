import axios from "axios";
import {
  Calculator,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  DatabaseZap,
  Hash,
  LoaderCircle,
  Plus,
  RadioTower,
  RefreshCw,
  Search,
  Tags,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { listTags } from "../../../services/tag.service";
import type {
  Tag,
  TagDataType,
  TagPagination,
  TagType,
} from "../../../types/tag";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./TagListPage.css";

type LoadState = "loading" | "ready" | "error";
type EnabledFilter = "all" | "enabled" | "disabled";

const tagsPerPage = 20;
const tagTypes: TagType[] = ["reading", "constant", "calculated"];
const dataTypes: TagDataType[] = [
  "bool",
  "int16",
  "uint16",
  "int32",
  "uint32",
  "float32",
  "float64",
];
const emptyPagination: TagPagination = {
  page: 1,
  per_page: tagsPerPage,
  total: 0,
  total_pages: 0,
};
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

function isTagType(value: string | null): value is TagType {
  return value !== null && tagTypes.includes(value as TagType);
}

function isTagDataType(value: string | null): value is TagDataType {
  return value !== null && dataTypes.includes(value as TagDataType);
}

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function tagTypeLabel(type: TagType): string {
  return type.charAt(0).toUpperCase() + type.slice(1);
}

function byteOrderLabel(value: string): string {
  return value
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function tagDefinition(entity: Tag): string {
  switch (entity.type) {
    case "reading": {
      const decoder = entity.config.decoder;
      const offset = decoder.config.byte_offset;
      return `${byteOrderLabel(decoder.config.byte_order)} · offset ${offset}`;
    }
    case "constant":
      return String(entity.config.value);
    case "calculated":
      return entity.config.expression;
  }
}

function sourceLabel(entity: Tag): string {
  if (entity.type !== "reading") {
    return "—";
  }
  return entity.datasource_id.length > 12
    ? `${entity.datasource_id.slice(0, 8)}…`
    : entity.datasource_id;
}

function formatUpdatedAt(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : dateFormatter.format(date);
}

function getErrorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as
      | { error?: { message?: string } }
      | undefined;
    if (data?.error?.message) {
      return data.error.message;
    }
  }
  return "Unable to load tags. Check the API connection and try again.";
}

function TagTypeIcon({ type }: { type: TagType }) {
  if (type === "reading") {
    return <RadioTower aria-hidden="true" size={19} />;
  }
  if (type === "calculated") {
    return <Calculator aria-hidden="true" size={19} />;
  }
  return <Hash aria-hidden="true" size={19} />;
}

function TagListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const querySearch = searchParams.get("search")?.trim() ?? "";
  const rawType = searchParams.get("type");
  const rawDataType = searchParams.get("data_type");
  const rawEnabled = searchParams.get("enabled");
  const selectedType = isTagType(rawType) ? rawType : undefined;
  const selectedDataType = isTagDataType(rawDataType)
    ? rawDataType
    : undefined;
  const enabledFilter: EnabledFilter =
    rawEnabled === "true"
      ? "enabled"
      : rawEnabled === "false"
        ? "disabled"
        : "all";
  const page = positivePage(searchParams.get("page"));

  const [tags, setTags] = useState<Tag[]>([]);
  const [pagination, setPagination] = useState(emptyPagination);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();

    async function load() {
      setLoadState("loading");
      setLoadError(null);
      try {
        const response = await listTags(
          {
            type: selectedType,
            data_type: selectedDataType,
            enabled:
              enabledFilter === "all"
                ? undefined
                : enabledFilter === "enabled",
            search: querySearch || undefined,
            page,
            per_page: tagsPerPage,
          },
          controller.signal,
        );
        if (!controller.signal.aborted) {
          setTags(response.data);
          setPagination(response.pagination);
          setLoadState("ready");
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setLoadError(getErrorMessage(error));
          setLoadState("error");
        }
      }
    }

    void load();
    return () => controller.abort();
  }, [enabledFilter, page, querySearch, refreshVersion, selectedDataType, selectedType]);

  const updateFilter = useCallback(
    (name: string, value?: string) => {
      const next = new URLSearchParams(searchParams);
      if (value) {
        next.set(name, value);
      } else {
        next.delete(name);
      }
      if (name !== "page") {
        next.delete("page");
      }
      setSearchParams(next, { replace: true });
    },
    [searchParams, setSearchParams],
  );

  const activeFilters = useMemo(
    () =>
      [selectedType, selectedDataType, enabledFilter !== "all", querySearch].filter(
        Boolean,
      ).length,
    [enabledFilter, querySearch, selectedDataType, selectedType],
  );
  const readingCount = tags.filter((entity) => entity.type === "reading").length;
  const calculatedCount = tags.filter(
    (entity) => entity.type === "calculated",
  ).length;

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = new FormData(event.currentTarget).get("search");
    const search = typeof value === "string" ? value.trim() : "";
    updateFilter("search", search || undefined);
  }

  function clearFilters() {
    setSearchParams({}, { replace: true });
  }

  return (
    <VGatewayShell
      breadcrumb={
        <>
          Dashboard <span>/</span> <strong>Tags</strong>
        </>
      }
    >
      <div className="tag-content">
        <section className="tag-heading">
          <div>
            <h1>Tags</h1>
            <p>Browse normalized values and calculation definitions across every datasource.</p>
          </div>
          <div className="tag-heading-actions">
            <span className="tag-heading-status">
              <DatabaseZap aria-hidden="true" size={18} />
              Global namespace
            </span>
            <Link className="tag-add-link" to="/tags/new">
              <Plus aria-hidden="true" size={18} />
              Add Tag
            </Link>
          </div>
        </section>

        <section className="tag-metrics" aria-label="Tag summary">
          <article>
            <Tags aria-hidden="true" />
            <span>Total tags</span>
            <strong>{pagination.total}</strong>
          </article>
          <article>
            <RadioTower aria-hidden="true" />
            <span>Reading on page</span>
            <strong>{readingCount}</strong>
          </article>
          <article>
            <Calculator aria-hidden="true" />
            <span>Calculated on page</span>
            <strong>{calculatedCount}</strong>
          </article>
        </section>

        <section className="tag-toolbar" aria-label="Tag filters">
          <form className="tag-search" role="search" onSubmit={submitSearch}>
            <Search aria-hidden="true" size={19} />
            <label className="sr-only" htmlFor="tag-search">Search tags</label>
            <input
              id="tag-search"
              key={querySearch}
              name="search"
              type="search"
              placeholder="Search tag names…"
              defaultValue={querySearch}
            />
            <button type="submit">Search</button>
          </form>
          <label>
            <span className="sr-only">Tag type</span>
            <select
              aria-label="Tag type"
              value={selectedType ?? "all"}
              onChange={(event) =>
                updateFilter("type", event.target.value === "all" ? undefined : event.target.value)
              }
            >
              <option value="all">All types</option>
              <option value="reading">Reading</option>
              <option value="constant">Constant</option>
              <option value="calculated">Calculated</option>
            </select>
          </label>
          <label>
            <span className="sr-only">Data type</span>
            <select
              aria-label="Data type"
              value={selectedDataType ?? "all"}
              onChange={(event) =>
                updateFilter("data_type", event.target.value === "all" ? undefined : event.target.value)
              }
            >
              <option value="all">All data types</option>
              {dataTypes.map((dataType) => (
                <option key={dataType} value={dataType}>{dataType}</option>
              ))}
            </select>
          </label>
          <label>
            <span className="sr-only">Enabled state</span>
            <select
              aria-label="Enabled state"
              value={enabledFilter}
              onChange={(event) => {
                const value = event.target.value as EnabledFilter;
                updateFilter(
                  "enabled",
                  value === "all" ? undefined : String(value === "enabled"),
                );
              }}
            >
              <option value="all">All states</option>
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
            </select>
          </label>
          <button
            className="tag-refresh"
            type="button"
            disabled={loadState === "loading"}
            onClick={() => setRefreshVersion((current) => current + 1)}
          >
            <RefreshCw
              aria-hidden="true"
              className={loadState === "loading" ? "is-spinning" : ""}
              size={18}
            />
            Refresh
          </button>
        </section>

        {activeFilters > 0 && (
          <div className="tag-active-filters">
            <span>{activeFilters} active {activeFilters === 1 ? "filter" : "filters"}</span>
            <button type="button" onClick={clearFilters}>Clear all</button>
          </div>
        )}

        <section className="tag-list-shell" aria-label="Tags">
          {loadState === "loading" && (
            <div className="tag-state" role="status">
              <LoaderCircle aria-hidden="true" className="is-spinning" size={27} />
              <strong>Loading tags…</strong>
              <span>Resolving the latest tag definitions.</span>
            </div>
          )}

          {loadState === "error" && (
            <div className="tag-state is-error" role="alert">
              <CircleAlert aria-hidden="true" size={29} />
              <strong>Couldn’t load tags</strong>
              <span>{loadError}</span>
              <button type="button" onClick={() => setRefreshVersion((current) => current + 1)}>
                Try again
              </button>
            </div>
          )}

          {loadState === "ready" && tags.length === 0 && (
            <div className="tag-state">
              {activeFilters > 0 ? <Search aria-hidden="true" size={29} /> : <Tags aria-hidden="true" size={30} />}
              <strong>{activeFilters > 0 ? "No matching tags" : "No tags yet"}</strong>
              <span>
                {activeFilters > 0
                  ? "Adjust or clear the filters to broaden the result."
                  : "Tag definitions will appear here once they are created."}
              </span>
              {activeFilters > 0 && <button type="button" onClick={clearFilters}>Clear filters</button>}
            </div>
          )}

          {loadState === "ready" && tags.length > 0 && (
            <>
              <div className="tag-table-scroll">
                <table>
                  <thead>
                    <tr>
                      <th>Name</th>
                      <th>Type</th>
                      <th>Data type</th>
                      <th>Source</th>
                      <th>Definition</th>
                      <th>Status</th>
                      <th>Updated</th>
                    </tr>
                  </thead>
                  <tbody>
                    {tags.map((entity) => (
                      <tr key={entity.id}>
                        <td data-label="Name">
                          <span className={`tag-name-icon is-${entity.type}`}>
                            <TagTypeIcon type={entity.type} />
                          </span>
                          <Link className="tag-name-link" to={`/tags/${entity.id}`}>
                            <strong>{entity.name}</strong>
                            <small>{entity.description ?? "Open real-time monitoring"}</small>
                          </Link>
                        </td>
                        <td data-label="Type">
                          <span className={`tag-type is-${entity.type}`}>{tagTypeLabel(entity.type)}</span>
                        </td>
                        <td data-label="Data type"><code>{entity.data_type}</code></td>
                        <td data-label="Source" title={entity.datasource_id ?? undefined}>{sourceLabel(entity)}</td>
                        <td data-label="Definition"><span className="tag-definition">{tagDefinition(entity)}</span></td>
                        <td data-label="Status">
                          <span className={entity.enabled ? "tag-enabled is-enabled" : "tag-enabled"}>
                            <span />
                            {entity.enabled ? "Enabled" : "Disabled"}
                          </span>
                        </td>
                        <td data-label="Updated">{formatUpdatedAt(entity.updated_at)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>

              <footer className="tag-pagination">
                <p>Showing {tags.length} of {pagination.total} tags</p>
                <div>
                  <button
                    type="button"
                    aria-label="Previous page"
                    disabled={page <= 1}
                    onClick={() => updateFilter("page", String(Math.max(1, page - 1)))}
                  >
                    <ChevronLeft aria-hidden="true" size={19} />
                  </button>
                  <span aria-label={`Page ${pagination.page}`}>{pagination.page}</span>
                  <button
                    type="button"
                    aria-label="Next page"
                    disabled={pagination.total_pages === 0 || page >= pagination.total_pages}
                    onClick={() => updateFilter("page", String(page + 1))}
                  >
                    <ChevronRight aria-hidden="true" size={19} />
                  </button>
                </div>
              </footer>
            </>
          )}
        </section>
      </div>
    </VGatewayShell>
  );
}

export default TagListPage;
