import { z } from "zod";
export declare const libraryOperations: readonly ["libraryItems", "reauthenticateLibrary", "libraryFacets", "semanticLibrarySearch", "libraryDiscovery", "libraryPins", "setLibraryPins", "libraryImportHistory", "discoveryItems", "updateMemoryPreference", "mergeDuplicates", "bulkLibraryItems", "duplicateLibraryItems", "libraryUsage", "agentUsage", "libraryAssetStacks", "createLibraryAssetStack", "updateLibraryAssetStack", "deleteLibraryAssetStack", "updateLibraryItem", "trashLibraryItem", "restoreLibraryItem", "setLibraryProviderImport", "promoteAttachment", "sharedReferences", "revokeLibraryGrant", "albums", "albumFolders", "createAlbumFolder", "updateAlbumFolder", "deleteAlbumFolder", "createAlbum", "organizeAlbum", "updateAlbum", "deleteAlbum", "albumItems", "addAlbumItems", "reorderAlbumItems", "groups", "createGroup", "groupItems", "peoplePolicy", "updatePeoplePolicy", "people", "createPerson", "updatePerson", "deletePerson", "personItems", "addPersonItems", "removePersonItems", "mergePeople", "editVersions", "createEditVersion", "renderEditVersion", "selectEditVersion", "deleteEditVersion"];
export declare const libraryReadOperations: readonly ["sharedReferenceContent", "libraryContent", "libraryOriginalContent", "libraryPreview", "libraryOriginalPreview"];
export declare const mistyLibraryContracts: {
    readonly "library.copyFiles": {
        readonly params: z.ZodObject<{
            files: z.ZodArray<z.ZodObject<{
                name: z.ZodString;
                bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodVoid;
    };
    readonly "library.perform": {
        readonly params: z.ZodObject<{
            operation: z.ZodEnum<{
                libraryItems: "libraryItems";
                reauthenticateLibrary: "reauthenticateLibrary";
                libraryFacets: "libraryFacets";
                semanticLibrarySearch: "semanticLibrarySearch";
                libraryDiscovery: "libraryDiscovery";
                libraryPins: "libraryPins";
                setLibraryPins: "setLibraryPins";
                libraryImportHistory: "libraryImportHistory";
                discoveryItems: "discoveryItems";
                updateMemoryPreference: "updateMemoryPreference";
                mergeDuplicates: "mergeDuplicates";
                bulkLibraryItems: "bulkLibraryItems";
                duplicateLibraryItems: "duplicateLibraryItems";
                libraryUsage: "libraryUsage";
                agentUsage: "agentUsage";
                libraryAssetStacks: "libraryAssetStacks";
                createLibraryAssetStack: "createLibraryAssetStack";
                updateLibraryAssetStack: "updateLibraryAssetStack";
                deleteLibraryAssetStack: "deleteLibraryAssetStack";
                updateLibraryItem: "updateLibraryItem";
                trashLibraryItem: "trashLibraryItem";
                restoreLibraryItem: "restoreLibraryItem";
                setLibraryProviderImport: "setLibraryProviderImport";
                promoteAttachment: "promoteAttachment";
                sharedReferences: "sharedReferences";
                revokeLibraryGrant: "revokeLibraryGrant";
                albums: "albums";
                albumFolders: "albumFolders";
                createAlbumFolder: "createAlbumFolder";
                updateAlbumFolder: "updateAlbumFolder";
                deleteAlbumFolder: "deleteAlbumFolder";
                createAlbum: "createAlbum";
                organizeAlbum: "organizeAlbum";
                updateAlbum: "updateAlbum";
                deleteAlbum: "deleteAlbum";
                albumItems: "albumItems";
                addAlbumItems: "addAlbumItems";
                reorderAlbumItems: "reorderAlbumItems";
                groups: "groups";
                createGroup: "createGroup";
                groupItems: "groupItems";
                peoplePolicy: "peoplePolicy";
                updatePeoplePolicy: "updatePeoplePolicy";
                people: "people";
                createPerson: "createPerson";
                updatePerson: "updatePerson";
                deletePerson: "deletePerson";
                personItems: "personItems";
                addPersonItems: "addPersonItems";
                removePersonItems: "removePersonItems";
                mergePeople: "mergePeople";
                editVersions: "editVersions";
                createEditVersion: "createEditVersion";
                renderEditVersion: "renderEditVersion";
                selectEditVersion: "selectEditVersion";
                deleteEditVersion: "deleteEditVersion";
            }>;
            args: z.ZodArray<z.ZodJSONSchema>;
        }, z.core.$strict>;
        readonly result: z.ZodUnion<[z.ZodJSONSchema, z.ZodUndefined]>;
    };
    readonly "library.read": {
        readonly params: z.ZodObject<{
            operation: z.ZodEnum<{
                sharedReferenceContent: "sharedReferenceContent";
                libraryContent: "libraryContent";
                libraryOriginalContent: "libraryOriginalContent";
                libraryPreview: "libraryPreview";
                libraryOriginalPreview: "libraryOriginalPreview";
            }>;
            args: z.ZodArray<z.ZodJSONSchema>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            mimeType: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "library.upload": {
        readonly params: z.ZodObject<{
            bytes: z.ZodCustom<ArrayBuffer, ArrayBuffer>;
            name: z.ZodString;
            mimeType: z.ZodString;
            purpose: z.ZodEnum<{
                library: "library";
                attachment: "attachment";
            }>;
            conversationId: z.ZodOptional<z.ZodString>;
            replace: z.ZodOptional<z.ZodObject<{
                itemId: z.ZodString;
                itemVersion: z.ZodNumber;
            }, z.core.$strict>>;
        }, z.core.$strict>;
        readonly result: z.ZodJSONSchema;
    };
};
export type MistyLibraryOperation = typeof libraryOperations[number];
export type MistyLibraryReadOperation = typeof libraryReadOperations[number];
export type MistyLibraryUpload = z.input<typeof mistyLibraryContracts["library.upload"]["params"]>;
export type MistyLibraryMethod = keyof typeof mistyLibraryContracts;
export declare const isMistyLibraryMethod: (method: string) => method is MistyLibraryMethod;
