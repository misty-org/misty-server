export function responseWithRequestID(
  response: Response,
  requestID: string,
): Response {
  if (response.status === 101) return response;
  // Responses proxied from Durable Objects can expose immutable headers even
  // for ordinary HTTP requests. Clone the response before adding correlation
  // metadata so a successful control command is not converted into error 1101.
  const correlated = new Response(response.body, response);
  correlated.headers.set("X-Request-ID", requestID);
  return correlated;
}
