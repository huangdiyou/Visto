import type {
  ItemList,
  ReviewTemplate,
  ReviewTemplateInput,
} from "@review-studio/contracts";
import { requestJSON, requestVoid } from "./client";

export async function listReviewTemplates(
  signal?: AbortSignal,
): Promise<ReviewTemplate[]> {
  const response = await requestJSON<ItemList<ReviewTemplate>>(
    "/api/v1/review-templates",
    signal ? { signal } : {},
  );
  return response.items;
}

export function createReviewTemplate(
  input: ReviewTemplateInput,
): Promise<ReviewTemplate> {
  return requestJSON<ReviewTemplate>("/api/v1/review-templates", {
    method: "POST",
    body: input,
  });
}

export function updateReviewTemplate(
  template: ReviewTemplate,
  input: ReviewTemplateInput,
): Promise<ReviewTemplate> {
  return requestJSON<ReviewTemplate>(
    `/api/v1/review-templates/${encodeURIComponent(template.id)}`,
    {
      method: "PATCH",
      body: { ...input, revision: template.revision },
    },
  );
}

export function deleteReviewTemplate(template: ReviewTemplate): Promise<void> {
  return requestVoid(
    `/api/v1/review-templates/${encodeURIComponent(template.id)}`,
    {
      method: "DELETE",
      body: { revision: template.revision },
    },
  );
}
