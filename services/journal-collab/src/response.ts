export function responseWithRequestID(
  response: Response,
  requestID: string,
): Response {
  if (response.status === 101) return response;
  const correlated = new Response(response.body, response);
  correlated.headers.set("X-Request-ID", requestID);
  return correlated;
}
