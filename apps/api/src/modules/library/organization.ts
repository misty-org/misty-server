import type { Pool } from "pg";
import { organizationTransactions } from "./organization-model.js";
import { createAlbums } from "./albums.js";
import { createAlbumFolders } from "./album-folders.js";
import { createAlbumItems } from "./album-items.js";
import { createLibraryGroups } from "./groups.js";
export function createLibraryOrganization(pool: Pool) {
  const transaction = organizationTransactions(pool);
  return { albums: createAlbums(transaction), folders: createAlbumFolders(transaction), items: createAlbumItems(transaction), groups: createLibraryGroups(transaction) };
}
