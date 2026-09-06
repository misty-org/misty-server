import { z } from "zod";
export type MistyViewState = null | boolean | number | string | MistyViewState[] | {
    [key: string]: MistyViewState;
};
export declare const MistyViewStateSchema: z.ZodCustom<MistyViewState, MistyViewState>;
export declare const MistyViewTitleSchema: z.ZodString;
export declare const MistyViewPlacementSchema: z.ZodEnum<{
    tab: "tab";
    left: "left";
    right: "right";
    up: "up";
    down: "down";
}>;
export declare const MistyWorkspaceViewSchema: z.ZodObject<{
    viewId: z.ZodString;
    panelId: z.ZodString;
    title: z.ZodString;
    route: z.ZodString;
    state: z.ZodCustom<MistyViewState, MistyViewState>;
    sidebarVisible: z.ZodBoolean;
    active: z.ZodBoolean;
    focused: z.ZodBoolean;
}, z.core.$strict>;
export declare const MistyWorkspaceSnapshotSchema: z.ZodObject<{
    revision: z.ZodNumber;
    views: z.ZodArray<z.ZodObject<{
        viewId: z.ZodString;
        panelId: z.ZodString;
        title: z.ZodString;
        route: z.ZodString;
        state: z.ZodCustom<MistyViewState, MistyViewState>;
        sidebarVisible: z.ZodBoolean;
        active: z.ZodBoolean;
        focused: z.ZodBoolean;
    }, z.core.$strict>>;
}, z.core.$strict>;
export declare const MistyWorkspaceUpdateSchema: z.ZodObject<{
    viewId: z.ZodString;
    state: z.ZodOptional<z.ZodCustom<MistyViewState, MistyViewState>>;
    title: z.ZodOptional<z.ZodString>;
    sidebarVisible: z.ZodOptional<z.ZodBoolean>;
}, z.core.$strict>;
export declare const MistyWorkspacePlaceSchema: z.ZodObject<{
    viewId: z.ZodString;
    targetViewId: z.ZodString;
    placement: z.ZodEnum<{
        tab: "tab";
        left: "left";
        right: "right";
        up: "up";
        down: "down";
    }>;
}, z.core.$strict>;
export declare const mistyWorkspaceContracts: {
    readonly "workspace.snapshot": {
        readonly params: z.ZodObject<{}, z.core.$strict>;
        readonly result: z.ZodObject<{
            revision: z.ZodNumber;
            views: z.ZodArray<z.ZodObject<{
                viewId: z.ZodString;
                panelId: z.ZodString;
                title: z.ZodString;
                route: z.ZodString;
                state: z.ZodCustom<MistyViewState, MistyViewState>;
                sidebarVisible: z.ZodBoolean;
                active: z.ZodBoolean;
                focused: z.ZodBoolean;
            }, z.core.$strict>>;
        }, z.core.$strict>;
    };
    readonly "workspace.update": {
        readonly params: z.ZodObject<{
            viewId: z.ZodString;
            state: z.ZodOptional<z.ZodCustom<MistyViewState, MistyViewState>>;
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
                tab: "tab";
                left: "left";
                right: "right";
                up: "up";
                down: "down";
            }>;
        }, z.core.$strict>;
        readonly result: z.ZodPipe<z.ZodUnion<readonly [z.ZodNull, z.ZodUndefined]>, z.ZodTransform<undefined, null | undefined>>;
    };
};
export type MistyWorkspaceSnapshot = z.output<typeof MistyWorkspaceSnapshotSchema>;
export type MistyWorkspaceView = z.output<typeof MistyWorkspaceViewSchema>;
export type MistyWorkspaceUpdate = z.input<typeof MistyWorkspaceUpdateSchema>;
export type MistyWorkspacePlace = z.input<typeof MistyWorkspacePlaceSchema>;
