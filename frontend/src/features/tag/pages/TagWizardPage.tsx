import {
  ArrowRight,
  Calculator,
  Check,
  DatabaseZap,
  Hash,
  RadioTower,
} from "lucide-react";
import { Navigate, useNavigate, useParams, useSearchParams } from "react-router-dom";

import type { TagType } from "../../../types/tag";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import CalculatedTagForm from "../components/CalculatedTagForm";
import ConstantTagForm from "../components/ConstantTagForm";
import ReadingTagForm from "../components/ReadingTagForm";
import "./TagWizardPage.css";

interface TagTypeOption {
  type: TagType;
  title: string;
  summary: string;
  detail: string;
}

const tagTypeOptions: TagTypeOption[] = [
  {
    type: "reading",
    title: "Reading Tag",
    summary: "Decode a live datasource value",
    detail: "Select a datasource, data type, byte order, and byte or bit offset.",
  },
  {
    type: "constant",
    title: "Constant Tag",
    summary: "Store a fixed reference value",
    detail: "Define a typed value for limits, rates, defaults, and configuration.",
  },
  {
    type: "calculated",
    title: "Calculated Tag",
    summary: "Derive a value from other tags",
    detail: "Build a typed expression with validated tag dependencies.",
  },
];

function isTagType(value: string | undefined | null): value is TagType {
  return tagTypeOptions.some((option) => option.type === value);
}

function typeIcon(type: TagType) {
  if (type === "reading") {
    return <RadioTower aria-hidden="true" size={25} />;
  }
  if (type === "calculated") {
    return <Calculator aria-hidden="true" size={25} />;
  }
  return <Hash aria-hidden="true" size={25} />;
}

function TagWizardPage() {
  const navigate = useNavigate();
  const { type: routeType } = useParams<{ type?: string }>();
  const [searchParams, setSearchParams] = useSearchParams();
  const selectedType = isTagType(searchParams.get("type"))
    ? searchParams.get("type") as TagType
    : undefined;

  if (routeType && !isTagType(routeType)) {
    return <Navigate replace to="/tags/new" />;
  }

  if (routeType) {
    return (
      <VGatewayShell breadcrumb={<><span>Tags</span> <span>/</span> <strong>Add Tag</strong></>}>
        <div className="tag-wizard-content">
          <WizardProgress activeStep={2} />
          {routeType === "reading" ? (
            <ReadingTagForm
              onCancel={() => navigate("/tags")}
              onChangeType={() => navigate("/tags/new?type=reading")}
              onCreated={() => navigate("/tags", { replace: true })}
            />
          ) : routeType === "constant" ? (
            <ConstantTagForm
              onCancel={() => navigate("/tags")}
              onChangeType={() => navigate("/tags/new?type=constant")}
              onCreated={() => navigate("/tags", { replace: true })}
            />
          ) : (
            <CalculatedTagForm
              onCancel={() => navigate("/tags")}
              onChangeType={() => navigate("/tags/new?type=calculated")}
              onCreated={() => navigate("/tags", { replace: true })}
            />
          )}
        </div>
      </VGatewayShell>
    );
  }

  function selectType(type: TagType) {
    setSearchParams({ type }, { replace: true });
  }

  return (
    <VGatewayShell breadcrumb={<><span>Tags</span> <span>/</span> <strong>Add Tag</strong></>}>
      <div className="tag-wizard-content">
        <WizardProgress activeStep={1} />
        <header className="tag-wizard-heading">
          <div>
            <span>New tag</span>
            <h1>Choose a tag type</h1>
            <p>Select how this named value will be produced. Tag type cannot be changed after creation.</p>
          </div>
          <span className="tag-wizard-namespace">
            <DatabaseZap aria-hidden="true" size={18} />
            Global namespace
          </span>
        </header>

        <section className="tag-type-grid" role="radiogroup" aria-label="Tag type">
          {tagTypeOptions.map((option) => {
            const selected = selectedType === option.type;
            return (
              <button
                key={option.type}
                className={`tag-type-card is-${option.type}${selected ? " is-selected" : ""}`}
                type="button"
                role="radio"
                aria-checked={selected}
                onClick={() => selectType(option.type)}
              >
                <span className="tag-wizard-icon">{typeIcon(option.type)}</span>
                <span className="tag-type-copy">
                  <strong>{option.title}</strong>
                  <small>{option.summary}</small>
                  <span>{option.detail}</span>
                </span>
                <span className="tag-type-check" aria-hidden="true">
                  {selected && <Check size={16} strokeWidth={3} />}
                </span>
              </button>
            );
          })}
        </section>

        <footer className="tag-wizard-actions">
          <button type="button" onClick={() => navigate("/tags")}>Cancel</button>
          <button
            className="is-primary"
            type="button"
            disabled={!selectedType}
            onClick={() => selectedType && navigate(`/tags/new/${selectedType}`)}
          >
            Continue
            <ArrowRight aria-hidden="true" size={17} />
          </button>
        </footer>
      </div>
    </VGatewayShell>
  );
}

function WizardProgress({ activeStep }: { activeStep: 1 | 2 }) {
  return (
    <ol className="tag-wizard-progress" aria-label="Tag creation progress">
      <li className={activeStep >= 1 ? "is-active" : ""} aria-current={activeStep === 1 ? "step" : undefined}>
        <span>{activeStep > 1 ? <Check aria-hidden="true" size={14} /> : "1"}</span>
        <div><strong>Tag type</strong><small>Choose a definition</small></div>
      </li>
      <li className={activeStep >= 2 ? "is-active" : ""} aria-current={activeStep === 2 ? "step" : undefined}>
        <span>2</span>
        <div><strong>Configuration</strong><small>Define and preview</small></div>
      </li>
    </ol>
  );
}

export default TagWizardPage;
