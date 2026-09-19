import type { ItemList, Membership } from "@review-studio/contracts";
import { requestJSON } from "./client";

export async function listMembers(signal?: AbortSignal): Promise<Membership[]> {
  const options: { signal?: AbortSignal } = {};
  if (signal) options.signal = signal;
  const result = await requestJSON<ItemList<Membership>>(
    "/api/v1/members",
    options,
  );
  return result.items;
}

export function updateMember(
  item: Membership,
  role: Membership["role"],
  status: Membership["status"],
): Promise<Membership> {
  return requestJSON<Membership>(`/api/v1/members/${item.id}`, {
    method: "PATCH",
    body: { role, status, revision: item.revision },
  });
}
