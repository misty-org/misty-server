import { afterAll, afterEach, beforeAll } from "vitest";
import { startCollab, stopAll, type Instance } from "./collab.js";
import { client, type Client, type ClientOptions, type Via } from "./sdk.js";
import { closeDatabase, unique, world, type World } from "./world.js";
import type { Person } from "./global_setup.js";

export interface Suite {
  instance: Instance;
  world: World;
  open(person: Person, key: string, options?: Partial<ClientOptions>): Promise<Client>;
  key(label: string): string;
}

export function suite(via: Via, label: string): Suite {
  const opened = new Set<Client>();
  const state = {} as Suite;
  beforeAll(async () => {
    state.instance = await startCollab();
    state.world = await world(label);
  });
  afterEach(() => {
    for (const each of opened) each.close();
    opened.clear();
  });
  afterAll(async () => {
    await stopAll();
    await closeDatabase();
  });
  state.key = (name: string) => unique(`${via}-${name}`);
  state.open = async (person, key, options = {}) => {
    const token = options.token ?? (await state.world.token(person)).token;
    const made = client({ via, url: state.instance.url, space: state.world.space, key, person, ...options, token });
    opened.add(made);
    const close = made.close;
    made.close = () => {
      opened.delete(made);
      close();
    };
    return made;
  };
  return state;
}
