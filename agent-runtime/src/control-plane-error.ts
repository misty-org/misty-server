export class ControlPlaneError extends Error {
  readonly transient: boolean;

  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
    this.name = "ControlPlaneError";
    this.transient = status === 408 || status === 429 || status >= 500;
  }
}
