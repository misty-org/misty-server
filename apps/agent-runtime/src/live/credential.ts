import { ModelSetupError, parseModelId, resolveTarget } from "../model-provider.js";
import type { LiveTicket } from "./ticket.js";
import { deviceRoute } from "../device-models.js";

/**
 * The Soniox key one live session runs on: the device's own, which the app gave this runtime.
 * The app minted the ticket, and the key lives only in the open socket's memory.
 */
export function liveSonioxKey(ticket: LiveTicket): string {
  if (parseModelId(ticket.model).provider !== "soniox")
    throw new ModelSetupError("model_unsupported", "Live transcription runs on Soniox models.");
  return resolveTarget(ticket.model, deviceRoute(ticket.model)).credential.apiKey;
}
