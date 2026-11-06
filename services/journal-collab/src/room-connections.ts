import type { Connection } from "partyserver";

/**
 * Closes a socket with the policy-violation code 1008. The code alone is sent;
 * nothing that identifies the document or its members goes over a closing socket.
 */
export function closeSocket(connection: Connection, code: string): void {
  try {
    connection.close(1008, code);
  } catch {
    /* already closing */
  }
}

export function countSockets(connections: Iterable<unknown>): number {
  let count = 0;
  for (const _ of connections) count += 1;
  return count;
}
