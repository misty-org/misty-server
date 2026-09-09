import { z } from "zod";
export declare const MistyDataDomainSchema: z.ZodEnum<{
    tasks: "tasks";
    calendar: "calendar";
    roadmaps: "roadmaps";
    notes: "notes";
    drawings: "drawings";
}>;
export type MistyDataDomain = z.infer<typeof MistyDataDomainSchema>;
export declare const MistyDataChangeSchema: z.ZodObject<{
    domain: z.ZodEnum<{
        tasks: "tasks";
        calendar: "calendar";
        roadmaps: "roadmaps";
        notes: "notes";
        drawings: "drawings";
    }>;
}, z.core.$strict>;
export declare const MistyTerminalPreferencesSchema: z.ZodObject<{
    cursorBlink: z.ZodBoolean;
    cursorStyleIndex: z.ZodNumber;
    fontFamily: z.ZodString;
    fontSize: z.ZodNumber;
    scrollback: z.ZodNumber;
}, z.core.$strict>;
export declare const MistyAppSettingsSchema: z.ZodObject<{
    code: z.ZodOptional<z.ZodObject<{
        autosaveDelayMs: z.ZodNumber;
        fontFamily: z.ZodString;
        fontSize: z.ZodNumber;
        formatOnSave: z.ZodBoolean;
        interfaceScale: z.ZodNumber;
        lineNumbers: z.ZodBoolean;
        tabSize: z.ZodNumber;
        theme: z.ZodString;
        wordWrap: z.ZodBoolean;
    }, z.core.$strict>>;
    terminal: z.ZodOptional<z.ZodObject<{
        cursorBlink: z.ZodBoolean;
        cursorStyleIndex: z.ZodNumber;
        fontFamily: z.ZodString;
        fontSize: z.ZodNumber;
        scrollback: z.ZodNumber;
    }, z.core.$strict>>;
    browser: z.ZodOptional<z.ZodObject<{
        homeUrl: z.ZodString;
        searchEngineIndex: z.ZodNumber;
    }, z.core.$strict>>;
    shortcutLabels: z.ZodOptional<z.ZodRecord<z.ZodString, z.ZodString>>;
}, z.core.$strict>;
export declare const mistyTerminalCommands: readonly ["terminal.clear", "terminal.search", "terminal.zoom_in", "terminal.zoom_out", "terminal.zoom_reset", "terminal.copy", "terminal.paste"];
export declare const mistyPlannerCommands: readonly ["planner.create", "roadmap.create", "roadmap.copy", "roadmap.paste", "roadmap.duplicate", "roadmap.delete", "roadmap.undo", "roadmap.redo"];
export declare const mistyBrowserCommands: readonly ["navigation.back", "navigation.forward", "navigation.refresh", "browser.annotation_undo", "browser.annotation_redo"];
export declare const mistyCodeCommands: readonly ["code.add_cursor_above", "code.add_cursor_below", "code.apply_inline_ai", "code.code_actions", "code.command_palette", "code.document_symbols", "code.format_document", "code.go_to_definition", "code.harpoon", "code.inline_ai", "code.open_multibuffer_excerpt", "code.previous_file", "code.quick_open", "code.references", "code.rename", "code.save", "code.search_project", "code.select_all_occurrences", "code.select_next_occurrence", "code.show_hover", "code.toggle_explorer", "code.toggle_terminal", "code.undo_selection"];
export declare const MistyAppCommandSchema: z.ZodEnum<{
    "terminal.clear": "terminal.clear";
    "terminal.search": "terminal.search";
    "terminal.zoom_in": "terminal.zoom_in";
    "terminal.zoom_out": "terminal.zoom_out";
    "terminal.zoom_reset": "terminal.zoom_reset";
    "terminal.copy": "terminal.copy";
    "terminal.paste": "terminal.paste";
    "planner.create": "planner.create";
    "roadmap.create": "roadmap.create";
    "roadmap.copy": "roadmap.copy";
    "roadmap.paste": "roadmap.paste";
    "roadmap.duplicate": "roadmap.duplicate";
    "roadmap.delete": "roadmap.delete";
    "roadmap.undo": "roadmap.undo";
    "roadmap.redo": "roadmap.redo";
    "navigation.back": "navigation.back";
    "navigation.forward": "navigation.forward";
    "navigation.refresh": "navigation.refresh";
    "browser.annotation_undo": "browser.annotation_undo";
    "browser.annotation_redo": "browser.annotation_redo";
    "code.add_cursor_above": "code.add_cursor_above";
    "code.add_cursor_below": "code.add_cursor_below";
    "code.apply_inline_ai": "code.apply_inline_ai";
    "code.code_actions": "code.code_actions";
    "code.command_palette": "code.command_palette";
    "code.document_symbols": "code.document_symbols";
    "code.format_document": "code.format_document";
    "code.go_to_definition": "code.go_to_definition";
    "code.harpoon": "code.harpoon";
    "code.inline_ai": "code.inline_ai";
    "code.open_multibuffer_excerpt": "code.open_multibuffer_excerpt";
    "code.previous_file": "code.previous_file";
    "code.quick_open": "code.quick_open";
    "code.references": "code.references";
    "code.rename": "code.rename";
    "code.save": "code.save";
    "code.search_project": "code.search_project";
    "code.select_all_occurrences": "code.select_all_occurrences";
    "code.select_next_occurrence": "code.select_next_occurrence";
    "code.show_hover": "code.show_hover";
    "code.toggle_explorer": "code.toggle_explorer";
    "code.toggle_terminal": "code.toggle_terminal";
    "code.undo_selection": "code.undo_selection";
}>;
export declare function commandsForApp(appId: string): readonly MistyAppCommand[];
export declare const MistyWorkspaceOpenSchema: z.ZodObject<{
    route: z.ZodString;
    placement: z.ZodDefault<z.ZodEnum<{
        down: "down";
        up: "up";
        left: "left";
        right: "right";
        tab: "tab";
    }>>;
    state: z.ZodOptional<z.ZodCustom<import("./workspace.js").MistyViewState, import("./workspace.js").MistyViewState>>;
    title: z.ZodOptional<z.ZodString>;
    sidebarVisible: z.ZodOptional<z.ZodBoolean>;
}, z.core.$strict>;
export type MistyWorkspaceOpen = z.input<typeof MistyWorkspaceOpenSchema>;
export declare const mistyAppUiContracts: {
    readonly "workspace.dirty.set": {
        readonly params: z.ZodObject<{
            dirty: z.ZodBoolean;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "workspace.open": {
        readonly params: z.ZodObject<{
            route: z.ZodString;
            placement: z.ZodDefault<z.ZodEnum<{
                down: "down";
                up: "up";
                left: "left";
                right: "right";
                tab: "tab";
            }>>;
            state: z.ZodOptional<z.ZodCustom<import("./workspace.js").MistyViewState, import("./workspace.js").MistyViewState>>;
            title: z.ZodOptional<z.ZodString>;
            sidebarVisible: z.ZodOptional<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodObject<{
            viewId: z.ZodString;
        }, z.core.$strict>;
    };
    readonly "dialogs.confirm": {
        readonly params: z.ZodObject<{
            message: z.ZodString;
            title: z.ZodOptional<z.ZodString>;
        }, z.core.$strict>;
        readonly result: z.ZodBoolean;
    };
    readonly "workspace.title.set": {
        readonly params: z.ZodObject<{
            title: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "settings.snapshot": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            code: z.ZodOptional<z.ZodObject<{
                autosaveDelayMs: z.ZodNumber;
                fontFamily: z.ZodString;
                fontSize: z.ZodNumber;
                formatOnSave: z.ZodBoolean;
                interfaceScale: z.ZodNumber;
                lineNumbers: z.ZodBoolean;
                tabSize: z.ZodNumber;
                theme: z.ZodString;
                wordWrap: z.ZodBoolean;
            }, z.core.$strict>>;
            terminal: z.ZodOptional<z.ZodObject<{
                cursorBlink: z.ZodBoolean;
                cursorStyleIndex: z.ZodNumber;
                fontFamily: z.ZodString;
                fontSize: z.ZodNumber;
                scrollback: z.ZodNumber;
            }, z.core.$strict>>;
            browser: z.ZodOptional<z.ZodObject<{
                homeUrl: z.ZodString;
                searchEngineIndex: z.ZodNumber;
            }, z.core.$strict>>;
            shortcutLabels: z.ZodOptional<z.ZodRecord<z.ZodString, z.ZodString>>;
        }, z.core.$strict>;
    };
    readonly "links.openExternal": {
        readonly params: z.ZodObject<{
            url: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "activity.report": {
        readonly params: z.ZodObject<{
            message: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "workspace.snapshot": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            revision: z.ZodNumber;
            views: z.ZodArray<z.ZodObject<{
                viewId: z.ZodString;
                panelId: z.ZodString;
                title: z.ZodString;
                route: z.ZodString;
                state: z.ZodCustom<import("./workspace.js").MistyViewState, import("./workspace.js").MistyViewState>;
                sidebarVisible: z.ZodBoolean;
                active: z.ZodBoolean;
                focused: z.ZodBoolean;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "workspace.update": {
        readonly params: z.ZodObject<{
            viewId: z.ZodString;
            state: z.ZodOptional<z.ZodCustom<import("./workspace.js").MistyViewState, import("./workspace.js").MistyViewState>>;
            title: z.ZodOptional<z.ZodString>;
            sidebarVisible: z.ZodOptional<z.ZodBoolean>;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "workspace.focus": {
        readonly params: z.ZodObject<{
            viewId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "workspace.close": {
        readonly params: z.ZodObject<{
            viewId: z.ZodString;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
    readonly "workspace.place": {
        readonly params: z.ZodObject<{
            viewId: z.ZodString;
            targetViewId: z.ZodString;
            placement: z.ZodEnum<{
                down: "down";
                up: "up";
                left: "left";
                right: "right";
                tab: "tab";
            }>;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyAppUiMethod = keyof typeof mistyAppUiContracts;
export type MistyAppUiParams<M extends MistyAppUiMethod> = z.input<(typeof mistyAppUiContracts)[M]["params"]>;
export type MistyAppUiResult<M extends MistyAppUiMethod> = z.output<(typeof mistyAppUiContracts)[M]["result"]>;
export type MistyAppSettings = z.infer<typeof MistyAppSettingsSchema>;
export type MistyTerminalPreferences = z.infer<typeof MistyTerminalPreferencesSchema>;
export type MistyAppCommand = z.infer<typeof MistyAppCommandSchema>;
export declare function isMistyAppUiMethod(method: string): method is MistyAppUiMethod;
