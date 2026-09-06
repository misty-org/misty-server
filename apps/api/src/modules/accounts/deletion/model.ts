export class AccountDeletionUnavailable extends Error {}
export class AccountDeletionAlreadyPending extends Error {}
export class AccountDeletionNotFound extends Error {}
export class AccountDeletionOwnership extends Error {
  constructor(readonly spaces: { space_id: string; name: string; member_count: number }[]) { super("account_deletion_space_ownership"); }
}
export type DeletionRequest = { id: string; status: string; purge_after: Date; provider_revocation_status: Record<string, string>;
  last_error_code: string; created_at: Date; updated_at: Date; completed_at: Date | null };
export const deletionRequestColumns = "id,status,purge_after,provider_revocation_status,last_error_code,created_at,updated_at,completed_at";
export function deletionResponse(row: DeletionRequest) {
  const { last_error_code, completed_at, ...rest } = row;
  return { ...rest, ...(last_error_code ? { last_error_code } : {}), ...(completed_at ? { completed_at } : {}) };
}
