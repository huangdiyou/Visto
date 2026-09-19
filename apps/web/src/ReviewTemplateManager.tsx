import { type FormEvent, useState } from "react";
import type {
  ReviewDecisionRule,
  ReviewTemplate,
  ReviewTemplateInput,
} from "@review-studio/contracts";
import {
  createReviewTemplate,
  deleteReviewTemplate,
  updateReviewTemplate,
} from "./api/review-templates";

interface TemplateDraft {
  name: string;
  description: string;
  participantRoles: Array<"reviewer" | "observer">;
  allowDownload: boolean;
  dueDays: string;
  decisionRule: ReviewDecisionRule;
}

export function ReviewTemplateManager({
  templates,
  onChange,
  onClose,
}: {
  templates: ReviewTemplate[];
  onChange: (templates: ReviewTemplate[]) => void;
  onClose: () => void;
}) {
  const [editingId, setEditingId] = useState<string | null>(null);
  const [draft, setDraft] = useState<TemplateDraft>(emptyDraft());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input = toInput(draft);
    if (!input.name || input.participantRoles.length === 0) {
      setError("请填写模板名称，并至少保留一个参与角色。");
      return;
    }
    if (
      input.decisionRule === "all_reviewers" &&
      !input.participantRoles.includes("reviewer")
    ) {
      setError("“全部审阅者通过”至少需要一个审阅者角色。");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      if (editingId) {
        const current = templates.find((item) => item.id === editingId);
        if (!current) return;
        const updated = await updateReviewTemplate(current, input);
        onChange(
          templates.map((item) => (item.id === updated.id ? updated : item)),
        );
      } else {
        const created = await createReviewTemplate(input);
        onChange([created, ...templates]);
      }
      setEditingId(null);
      setDraft(emptyDraft());
    } catch (submitError) {
      setError(errorMessage(submitError, "无法保存审阅模板"));
    } finally {
      setBusy(false);
    }
  }

  async function remove(template: ReviewTemplate) {
    if (!window.confirm(`删除模板“${template.name}”？已有审阅不会受影响。`)) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deleteReviewTemplate(template);
      onChange(templates.filter((item) => item.id !== template.id));
      if (editingId === template.id) {
        setEditingId(null);
        setDraft(emptyDraft());
      }
    } catch (deleteError) {
      setError(errorMessage(deleteError, "无法删除审阅模板"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="review-template-manager">
      <header className="project-detail-header">
        <div>
          <p className="eyebrow">REVIEW WORKFLOWS</p>
          <h2>审阅模板</h2>
          <p>更改仅用于新审阅。</p>
        </div>
        <button className="secondary-button" type="button" onClick={onClose}>
          返回审阅
        </button>
      </header>

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}

      <form className="review-template-form" onSubmit={submit}>
        <div className="review-form-grid">
          <label className="field">
            <span>模板名称</span>
            <input
              maxLength={120}
              value={draft.name}
              onChange={(event) =>
                setDraft({ ...draft, name: event.target.value })
              }
            />
          </label>
          <label className="field">
            <span>默认截止天数</span>
            <input
              type="number"
              min={1}
              max={365}
              placeholder="不设置"
              value={draft.dueDays}
              onChange={(event) =>
                setDraft({ ...draft, dueDays: event.target.value })
              }
            />
          </label>
        </div>
        <label className="field">
          <span>说明</span>
          <textarea
            maxLength={500}
            value={draft.description}
            onChange={(event) =>
              setDraft({ ...draft, description: event.target.value })
            }
          />
        </label>
        <div className="review-template-options">
          <label className="field">
            <span>决策规则</span>
            <select
              value={draft.decisionRule}
              onChange={(event) =>
                setDraft({
                  ...draft,
                  decisionRule: event.target.value as ReviewDecisionRule,
                })
              }
            >
              <option value="any_reviewer">任一审阅者可决定</option>
              <option value="all_reviewers">全部审阅者通过</option>
              <option value="responsible_only">仅负责人可决定</option>
            </select>
          </label>
          <label className="review-template-download">
            <input
              type="checkbox"
              checked={draft.allowDownload}
              onChange={(event) =>
                setDraft({ ...draft, allowDownload: event.target.checked })
              }
            />
            <span>分享时默认允许下载源文件</span>
          </label>
        </div>

        <section className="review-template-roles">
          <div className="section-heading">
            <div>
              <p className="eyebrow">ROLE SLOTS</p>
              <h2>参与角色</h2>
            </div>
            <button
              className="secondary-button"
              type="button"
              onClick={() =>
                setDraft({
                  ...draft,
                  participantRoles: [...draft.participantRoles, "reviewer"],
                })
              }
            >
              添加角色
            </button>
          </div>
          <div className="review-template-role-list">
            {draft.participantRoles.map((role, index) => (
              <div key={index}>
                <span>角色 {index + 1}</span>
                <select
                  value={role}
                  onChange={(event) => {
                    const roles = [...draft.participantRoles];
                    roles[index] = event.target.value as
                      | "reviewer"
                      | "observer";
                    setDraft({ ...draft, participantRoles: roles });
                  }}
                >
                  <option value="reviewer">审阅者</option>
                  <option value="observer">观察者</option>
                </select>
                <button
                  className="text-danger-button"
                  type="button"
                  onClick={() =>
                    setDraft({
                      ...draft,
                      participantRoles: draft.participantRoles.filter(
                        (_, roleIndex) => roleIndex !== index,
                      ),
                    })
                  }
                >
                  移除
                </button>
              </div>
            ))}
          </div>
        </section>

        <div className="editor-actions review-editor-actions">
          {editingId ? (
            <button
              className="secondary-button"
              type="button"
              disabled={busy}
              onClick={() => {
                setEditingId(null);
                setDraft(emptyDraft());
              }}
            >
              取消编辑
            </button>
          ) : null}
          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "保存中" : editingId ? "保存模板" : "创建模板"}
          </button>
        </div>
      </form>

      <section className="review-template-library">
        <div className="section-heading">
          <div>
            <p className="eyebrow">TEMPLATE LIBRARY</p>
            <h2>可用模板</h2>
          </div>
          <strong>{templates.length}</strong>
        </div>
        {templates.length === 0 ? (
          <p className="review-muted">还没有审阅模板。</p>
        ) : (
          <div className="review-template-list">
            {templates.map((template) => (
              <article key={template.id}>
                <div>
                  <strong>{template.name}</strong>
                  <span>
                    {template.participantRoles.length} 个角色 ·{" "}
                    {decisionRuleLabel(template.decisionRule)} · V
                    {template.revision}
                  </span>
                </div>
                <div className="detail-actions">
                  <button
                    className="secondary-button"
                    type="button"
                    disabled={busy}
                    onClick={() => {
                      setEditingId(template.id);
                      setDraft(fromTemplate(template));
                      setError(null);
                    }}
                  >
                    编辑
                  </button>
                  <button
                    className="text-danger-button"
                    type="button"
                    disabled={busy}
                    onClick={() => void remove(template)}
                  >
                    删除
                  </button>
                </div>
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function emptyDraft(): TemplateDraft {
  return {
    name: "",
    description: "",
    participantRoles: ["reviewer"],
    allowDownload: false,
    dueDays: "",
    decisionRule: "any_reviewer",
  };
}

function fromTemplate(template: ReviewTemplate): TemplateDraft {
  return {
    name: template.name,
    description: template.description ?? "",
    participantRoles: [...template.participantRoles],
    allowDownload: template.allowDownload,
    dueDays: template.dueDays?.toString() ?? "",
    decisionRule: template.decisionRule,
  };
}

function toInput(draft: TemplateDraft): ReviewTemplateInput {
  const dueDays = Number.parseInt(draft.dueDays, 10);
  return {
    name: draft.name.trim(),
    description: draft.description.trim() || null,
    participantRoles: draft.participantRoles,
    allowDownload: draft.allowDownload,
    dueDays: Number.isFinite(dueDays) ? dueDays : null,
    decisionRule: draft.decisionRule,
  };
}

function decisionRuleLabel(rule: ReviewDecisionRule) {
  switch (rule) {
    case "all_reviewers":
      return "全部审阅者通过";
    case "responsible_only":
      return "仅负责人决定";
    default:
      return "任一审阅者决定";
  }
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error && error.message ? error.message : fallback;
}
