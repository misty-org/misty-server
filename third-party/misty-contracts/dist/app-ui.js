import { MistyEditorPreferencesSchema } from "./code-controls.js";
import { mistyWorkspaceContracts, MistyViewStateSchema, MistyViewTitleSchema, MistyViewPlacementSchema } from "./workspace.js";
import { z } from "zod";
const label = z
    .string()
    .min(1)
    .max(160)
    .refine((value) => !/[\u0000-\u001f\u007f]/.test(value));
export const MistyDataDomainSchema = z.enum(["tasks", "calendar", "roadmaps", "notes", "drawings"]);
export const MistyDataChangeSchema = z.strictObject({
    domain: MistyDataDomainSchema,
});
export const MistyTerminalPreferencesSchema = z.strictObject({
    cursorBlink: z.boolean(),
    cursorStyleIndex: z.number().int().min(0).max(2),
    fontFamily: z.string().max(512),
    fontSize: z.number().min(8).max(32),
    scrollback: z.number().int().min(1000).max(500000),
});
export const MistyAppSettingsSchema = z.strictObject({
    code: MistyEditorPreferencesSchema.optional(),
    terminal: MistyTerminalPreferencesSchema.optional(),
    browser: z.strictObject({
        homeUrl: z.string().max(8192),
        searchEngineIndex: z.number().int().min(0).max(4),
    }).optional(),
    shortcutLabels: z.record(z.string(), z.string().max(160)).optional(),
});
export const mistyTerminalCommands = [
    "terminal.clear",
    "terminal.search",
    "terminal.zoom_in",
    "terminal.zoom_out",
    "terminal.zoom_reset",
    "terminal.copy",
    "terminal.paste",
];
export const mistyPlannerCommands = [
    "planner.create",
    "roadmap.create",
    "roadmap.copy",
    "roadmap.paste",
    "roadmap.duplicate",
    "roadmap.delete",
    "roadmap.undo",
    "roadmap.redo",
];
export const mistyBrowserCommands = [
    "navigation.back", "navigation.forward", "navigation.refresh",
    "browser.annotation_undo", "browser.annotation_redo",
];
export const mistyCodeCommands = ["code.add_cursor_above", "code.add_cursor_below", "code.apply_inline_ai", "code.code_actions", "code.command_palette", "code.document_symbols", "code.format_document", "code.go_to_definition", "code.harpoon", "code.inline_ai", "code.open_multibuffer_excerpt", "code.previous_file", "code.quick_open", "code.references", "code.rename", "code.save", "code.search_project", "code.select_all_occurrences", "code.select_next_occurrence", "code.show_hover", "code.toggle_explorer", "code.toggle_terminal", "code.undo_selection"];
export const MistyAppCommandSchema = z.enum([
    ...mistyTerminalCommands,
    ...mistyPlannerCommands,
    ...mistyBrowserCommands,
    ...mistyCodeCommands,
]);
export function commandsForApp(appId) {
    switch (appId) {
        case "code":
            return mistyCodeCommands;
        case "terminal":
            return mistyTerminalCommands;
        case "planner":
            return mistyPlannerCommands;
        case "browser":
            return mistyBrowserCommands;
        case "chat":
        case "inbox":
            return ["navigation.back", "navigation.forward", "navigation.refresh"];
        default:
            return [];
    }
}
const empty = z.strictObject({});
export const MistyWorkspaceOpenSchema = z.strictObject({
    route: z.string().min(1).max(2048).refine((value) => value.startsWith("/apps/") && !/[\\\u0000-\u001f\u007f]/.test(value)),
    placement: MistyViewPlacementSchema.default("tab"),
    state: MistyViewStateSchema.optional(),
    title: MistyViewTitleSchema.optional(),
    sidebarVisible: z.boolean().optional(),
});
const voidResult = z
    .union([z.null(), z.undefined()])
    .transform(() => undefined);
export const mistyAppUiContracts = {
    ...mistyWorkspaceContracts,
    "workspace.dirty.set": { params: z.strictObject({ dirty: z.boolean() }), result: voidResult },
    "workspace.open": {
        params: MistyWorkspaceOpenSchema,
        result: z.strictObject({ viewId: label }),
    },
    "dialogs.confirm": {
        params: z.strictObject({
            message: z.string().min(1).max(2000),
            title: label.optional(),
        }),
        result: z.boolean(),
    },
    "workspace.title.set": {
        params: z.strictObject({ title: label }),
        result: voidResult,
    },
    "settings.snapshot": { params: empty, result: MistyAppSettingsSchema },
    "links.openExternal": {
        params: z.strictObject({
            url: z
                .string()
                .max(4096)
                .refine((value) => {
                try {
                    const url = new URL(value);
                    return (["https:", "http:", "mailto:"].includes(url.protocol) &&
                        !url.username &&
                        !url.password);
                }
                catch {
                    return false;
                }
            }),
        }),
        result: voidResult,
    },
    "activity.report": {
        params: z.strictObject({ message: z.string().min(1).max(2000) }),
        result: voidResult,
    },
};
export function isMistyAppUiMethod(method) {
    return Object.hasOwn(mistyAppUiContracts, method);
}
//# sourceMappingURL=app-ui.js.map