import * as Y from "yjs";
import {
  connect,
  openRoomClient,
  requestTransport,
  streamTransport,
  webSocketTransport,
  type FileStore,
  type MistyClient,
  type RoomClient,
  type RoomWireOptions,
  type TokenSource,
  type Transport,
} from "../../../../misty-sdk/packages/sdk/src/index.js";
import type { Person } from "./global_setup.js";
import { Recorder } from "./wait.js";

export * from "../../../../misty-sdk/packages/sdk/src/index.js";

export type Via = "websocket" | "stream";

export const VIAS: Via[] = ["websocket", "stream"];

export type TokenLike = string | TokenSource;

const sourceOf = (token: TokenLike): TokenSource => (typeof token === "string" ? () => Promise.resolve(token) : token);

export interface Closes {
  code: number;
  reason: string;
}

export interface Watched {
  transport: Transport;
  closes: Recorder<Closes>;
  opens: Recorder<string>;
  frames: Recorder<string>;
}

export function transportFor(via: Via, url: string, token: TokenLike): Watched {
  const source = sourceOf(token);
  const live = via === "websocket" ? webSocketTransport({ url, token: source }) : streamTransport({ url, token: source });
  const closes = new Recorder<Closes>();
  const opens = new Recorder<string>();
  const frames = new Recorder<string>();
  const transport: Transport = {
    open(request, events) {
      return live.open(request, {
        opened(said) {
          opens.push(request.url ?? url);
          events.opened(said);
        },
        binary(frame) {
          frames.push("binary");
          events.binary(frame);
        },
        text(message) {
          frames.push(typeof message.type === "string" ? message.type : "text");
          events.text(message);
        },
        closed(code, reason) {
          closes.push({ code, reason });
          events.closed(code, reason);
        },
      });
    },
    request: requestTransport({ url, token: source }),
  };
  return { transport, closes, opens, frames };
}

export interface Client {
  room: RoomClient;
  doc: Y.Doc;
  messages: Recorder<Record<string, unknown>>;
  file: Recorder<Record<string, unknown>>;
  closes: Recorder<Closes>;
  opens: Recorder<string>;
  frames: Recorder<string>;
  problems: Recorder<{ code: string; message: string }>;
  fileAtClose: number[];
  send(message: Record<string, unknown>): boolean;
  last(type: string): Record<string, unknown> | undefined;
  close(): void;
}

export interface ClientOptions {
  via: Via;
  url: string;
  space: string;
  key: string;
  token: TokenLike;
  person?: Person;
  options?: RoomWireOptions;
  doc?: Y.Doc;
}

export function client(options: ClientOptions): Client {
  const watched = transportFor(options.via, options.url, options.token);
  const room = openRoomClient({
    transport: watched.transport,
    space: options.space,
    key: options.key,
    ...(options.options ? { options: options.options } : {}),
    ...(options.person ? { identity: { user_id: options.person.id, name: options.person.username, tone: null } } : {}),
    ...(options.doc ? { doc: options.doc } : {}),
  });
  const messages = new Recorder<Record<string, unknown>>();
  const file = new Recorder<Record<string, unknown>>();
  const problems = new Recorder<{ code: string; message: string }>();
  room.onMessage((message) => {
    messages.push(message);
    if (message.option === "file") file.push(message);
  });
  room.onProblem((problem) => problems.push({ code: problem.code, message: problem.message }));
  const fileAtClose: number[] = [];
  watched.closes.source(() => fileAtClose.push(file.items.length));
  return {
    room,
    doc: room.doc,
    messages,
    file,
    closes: watched.closes,
    opens: watched.opens,
    frames: watched.frames,
    problems,
    fileAtClose,
    send: (message) => room.send({ option: "file", ...message }),
    last: (type) => file.items.filter((message) => message.type === type).at(-1),
    close: () => room.close(),
  };
}

export async function direct(options: {
  via: Via;
  url: string;
  space: string;
  token: TokenLike;
  person: Person;
  files?: FileStore;
}): Promise<MistyClient & Watched> {
  const watched = transportFor(options.via, options.url, options.token);
  const misty = await connect({
    transport: watched.transport,
    space: options.space,
    identity: { user_id: options.person.id, name: options.person.username, tone: null },
    ...(options.files ? { files: options.files } : {}),
  });
  return Object.assign(misty, watched);
}
