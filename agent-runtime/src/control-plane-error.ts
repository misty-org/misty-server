export class ControlPlaneError extends Error {
  readonly transient: boolean;

  constructor(
    readonly status: number,
    message: string,
    readonly code = "control_plane_error",
  ) {
    super(message);
    this.name = "ControlPlaneError";
    this.transient =
      code !== "hosted_ai_limit_reached" &&
      (status === 408 || status === 429 || status >= 500);
  }
}
