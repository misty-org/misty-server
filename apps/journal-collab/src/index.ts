import { routePartykitRequest } from "partyserver";

import { DrawingRoom, NoteRoom, type Env } from "./room";
import { responseWithRequestID } from "./response";

export { DrawingRoom, NoteRoom };

export default {
  async fetch(request: Request, env: Env): Promise<Response> {
    const providedRequestID = request.headers.get("X-Request-ID") ?? "";
    const requestID = /^[A-Za-z0-9][A-Za-z0-9._:-]{7,127}$/.test(
      providedRequestID,
    )
      ? providedRequestID
      : `worker_${crypto.randomUUID()}`;
    const headers = new Headers(request.headers);
    headers.set("X-Request-ID", requestID);
    const correlatedRequest = new Request(request, { headers });
    const response =
      (await routePartykitRequest(
        correlatedRequest,
        env as unknown as Record<string, unknown>,
      )) ?? new Response("not found", { status: 404 });
    return responseWithRequestID(response, requestID);
  },
};
