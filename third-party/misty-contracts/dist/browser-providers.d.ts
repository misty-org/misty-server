/** Shared website navigation policy. Native hosts consume the same registry snapshot. */
export declare const mistyBrowserProviders: {
    readonly "google-drive": {
        readonly owner: "library";
        readonly url: "https://drive.google.com/drive/u/0/my-drive";
        readonly domains: readonly ["drive.google.com", "docs.google.com"];
        readonly auth: readonly ["accounts.google.com"];
    };
    readonly dropbox: {
        readonly owner: "library";
        readonly url: "https://www.dropbox.com/home";
        readonly domains: readonly ["dropbox.com"];
        readonly auth: readonly ["accounts.google.com", "login.live.com", "login.microsoftonline.com", "appleid.apple.com"];
    };
    readonly onedrive: {
        readonly owner: "library";
        readonly url: "https://onedrive.live.com/";
        readonly domains: readonly ["onedrive.live.com", "onedrive.com", "sharepoint.com", "m365.cloud.microsoft"];
        readonly auth: readonly ["login.live.com", "login.microsoftonline.com", "account.live.com"];
    };
    readonly google: {
        readonly owner: "inbox";
        readonly url: "https://mail.google.com/mail/u/0/#inbox";
        readonly domains: readonly ["mail.google.com"];
        readonly auth: readonly ["accounts.google.com"];
    };
    readonly microsoft: {
        readonly owner: "inbox";
        readonly url: "https://outlook.live.com/mail/";
        readonly domains: readonly ["outlook.live.com", "outlook.office.com", "outlook.office365.com", "outlook.cloud.microsoft"];
        readonly auth: readonly ["login.live.com", "login.microsoftonline.com", "account.live.com"];
    };
    readonly instagram: {
        readonly owner: "chat";
        readonly url: "https://www.instagram.com/direct/inbox/";
        readonly domains: readonly ["instagram.com"];
        readonly auth: readonly ["accountscenter.instagram.com", "facebook.com"];
    };
    readonly messenger: {
        readonly owner: "chat";
        readonly url: "https://www.messenger.com/";
        readonly domains: readonly ["messenger.com"];
        readonly auth: readonly ["www.facebook.com", "web.facebook.com"];
    };
    readonly x: {
        readonly owner: "chat";
        readonly url: "https://x.com/messages";
        readonly domains: readonly ["x.com", "twitter.com"];
        readonly auth: readonly ["accounts.google.com", "appleid.apple.com", "idmsa.apple.com"];
    };
    readonly discord: {
        readonly owner: "chat";
        readonly url: "https://discord.com/channels/@me";
        readonly domains: readonly ["discord.com"];
        readonly auth: readonly [];
    };
    readonly slack: {
        readonly owner: "chat";
        readonly url: "https://slack.com/signin#/signin";
        readonly domains: readonly ["slack.com"];
        readonly auth: readonly ["accounts.google.com", "appleid.apple.com", "idmsa.apple.com", "login.microsoftonline.com", "login.live.com"];
    };
    readonly "microsoft-teams": {
        readonly owner: "chat";
        readonly url: "https://teams.microsoft.com/";
        readonly domains: readonly ["teams.microsoft.com", "teams.live.com", "teams.cloud.microsoft"];
        readonly auth: readonly ["login.microsoftonline.com", "login.live.com", "account.live.com"];
    };
    readonly icloud: {
        readonly owner: "inbox";
        readonly url: "https://www.icloud.com/mail/";
        readonly domains: readonly ["icloud.com"];
        readonly auth: readonly ["idmsa.apple.com", "appleid.apple.com", "account.apple.com"];
    };
    readonly yahoo: {
        readonly owner: "inbox";
        readonly url: "https://mail.yahoo.com/";
        readonly domains: readonly ["mail.yahoo.com"];
        readonly auth: readonly ["login.yahoo.com"];
    };
    readonly "google-docs": {
        readonly owner: "journal";
        readonly url: "https://docs.google.com/document/";
        readonly domains: readonly ["docs.google.com", "drive.google.com"];
        readonly auth: readonly ["accounts.google.com"];
    };
    readonly "microsoft-word": {
        readonly owner: "journal";
        readonly url: "https://word.cloud.microsoft/";
        readonly domains: readonly ["word.cloud.microsoft", "word.office.com", "word-edit.officeapps.live.com", "m365.cloud.microsoft", "www.office.com", "www.microsoft365.com", "onedrive.live.com", "sharepoint.com"];
        readonly auth: readonly ["login.live.com", "login.microsoftonline.com", "account.live.com"];
    };
    readonly notion: {
        readonly owner: "journal";
        readonly url: "https://www.notion.so/";
        readonly domains: readonly ["notion.so", "notion.site"];
        readonly auth: readonly ["accounts.google.com", "login.live.com", "login.microsoftonline.com", "account.live.com", "appleid.apple.com", "idmsa.apple.com"];
    };
    readonly "microsoft-onenote": {
        readonly owner: "journal";
        readonly url: "https://www.onenote.com/notebooks";
        readonly domains: readonly ["onenote.com", "onenote.cloud.microsoft", "onenote.officeapps.live.com", "m365.cloud.microsoft", "www.office.com", "www.microsoft365.com", "onedrive.live.com", "sharepoint.com"];
        readonly auth: readonly ["login.live.com", "login.microsoftonline.com", "account.live.com"];
    };
    readonly "google-calendar": {
        readonly owner: "planner";
        readonly url: "https://calendar.google.com/calendar/u/0/r";
        readonly domains: readonly ["calendar.google.com"];
        readonly auth: readonly ["accounts.google.com", "workspace.google.com"];
    };
    readonly "outlook-calendar": {
        readonly owner: "planner";
        readonly url: "https://outlook.live.com/calendar/";
        readonly domains: readonly ["outlook.live.com", "outlook.office.com", "outlook.office365.com", "outlook.cloud.microsoft"];
        readonly auth: readonly ["login.live.com", "login.microsoftonline.com", "account.live.com"];
    };
    readonly "microsoft-todo": {
        readonly owner: "planner";
        readonly url: "https://to-do.live.com/tasks/";
        readonly domains: readonly ["to-do.live.com", "to-do.office.com", "todo.cloud.microsoft"];
        readonly auth: readonly ["login.live.com", "login.microsoftonline.com", "account.live.com"];
    };
    readonly todoist: {
        readonly owner: "planner";
        readonly url: "https://app.todoist.com/";
        readonly domains: readonly ["todoist.com"];
        readonly auth: readonly ["accounts.google.com", "login.live.com", "login.microsoftonline.com", "account.live.com", "appleid.apple.com", "idmsa.apple.com"];
    };
    readonly trello: {
        readonly owner: "planner";
        readonly url: "https://trello.com/";
        readonly domains: readonly ["trello.com"];
        readonly auth: readonly ["id.atlassian.com", "auth.atlassian.com", "accounts.google.com", "login.live.com", "login.microsoftonline.com", "account.live.com", "appleid.apple.com", "idmsa.apple.com"];
    };
    readonly asana: {
        readonly owner: "planner";
        readonly url: "https://app.asana.com/";
        readonly domains: readonly ["asana.com"];
        readonly auth: readonly ["accounts.google.com", "login.live.com", "login.microsoftonline.com", "account.live.com", "appleid.apple.com", "idmsa.apple.com"];
    };
    readonly jira: {
        readonly owner: "planner";
        readonly url: "https://id.atlassian.com/";
        readonly domains: readonly ["atlassian.net"];
        readonly auth: readonly ["id.atlassian.com", "auth.atlassian.com", "api.atlassian.com", "accounts.google.com", "login.live.com", "login.microsoftonline.com", "account.live.com", "appleid.apple.com", "idmsa.apple.com"];
    };
};
export type MistyBrowserProviderId = keyof typeof mistyBrowserProviders;
