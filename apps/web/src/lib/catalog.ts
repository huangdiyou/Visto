export interface CatalogDraft {
  name: string;
  description: string;
}

export function validateCatalogDraft(draft: CatalogDraft): string | null {
  const nameLength = [...draft.name.trim()].length;
  if (nameLength < 1 || nameLength > 120) {
    return "名称需要 1 到 120 个字符";
  }
  if ([...draft.description.trim()].length > 2000) {
    return "描述最多 2000 个字符";
  }
  return null;
}

export function nullableDescription(value: string): string | null {
  const trimmed = value.trim();
  return trimmed === "" ? null : trimmed;
}
