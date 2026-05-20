import { fetchApi } from "@/lib/api";
import type { UpdateMeBody, User } from "@/types/api";

/** PATCH /api/v1/users/me */
export async function updateMe(body: UpdateMeBody): Promise<User> {
  return fetchApi<User>("/api/v1/users/me", {
    method: "PATCH",
    json: body,
  });
}
