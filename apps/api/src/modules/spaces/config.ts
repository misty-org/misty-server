/** Public configuration flags only; credentials stay in the integration owner. */
export function loadTemplateProviders(env: NodeJS.ProcessEnv) {
  return [{ provider: "github", configured: ["GITHUB_APP_ID", "GITHUB_APP_SLUG", "GITHUB_APP_PRIVATE_KEY", "GITHUB_WEBHOOK_SECRET"].every((key) => !!env[key]?.trim()) }];
}
