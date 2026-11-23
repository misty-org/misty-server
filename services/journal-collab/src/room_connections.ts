import type { Connection } from "partyserver";

export function closeSocket(connection: Connection, code: string): void {
  try {
    connection.close(1008, code);
  } catch {
  }
}

export function countSockets(connections: Iterable<unknown>): number {
  let count = 0;
  for (const _ of connections) count += 1;
  return count;
}
