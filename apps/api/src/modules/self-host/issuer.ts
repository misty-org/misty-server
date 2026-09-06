import { createHmac, createPrivateKey, randomUUID, type KeyObject } from "node:crypto";
import { SignJWT } from "jose";

const maximumLifetimeSeconds = 7 * 86400;
export type SelfHostSigningConfig = { privateKey: KeyObject; keyId: string; subjectSecret: Buffer };

/** Keep the Go PKCS8 and subject-secret formats so existing account bindings survive migration. */
export function loadSelfHostSigningConfig(env: NodeJS.ProcessEnv): SelfHostSigningConfig | null {
  const encodedKey = env.MISTY_SELF_HOST_ENTITLEMENT_PRIVATE_KEY?.trim() ?? "";
  const keyId = env.MISTY_SELF_HOST_ENTITLEMENT_KEY_ID?.trim() ?? "";
  const encodedSecret = env.MISTY_SELF_HOST_ENTITLEMENT_SUBJECT_SECRET?.trim() ?? "";
  if (!encodedKey && !keyId && !encodedSecret) return null;
  try {
    const decode = (value: string) => {
      if (!value || value.length > 8192) throw new Error();
      const bytes = Buffer.from(value, "base64");
      if (bytes.toString("base64") !== value) throw new Error();
      return bytes;
    };
    const privateKey = createPrivateKey({ key: decode(encodedKey), format: "der", type: "pkcs8" });
    const subjectSecret = decode(encodedSecret);
    if (privateKey.asymmetricKeyType !== "ed25519" || !keyId || keyId.length > 100 || subjectSecret.length < 32) throw new Error();
    return { privateKey, keyId, subjectSecret };
  } catch { throw new Error("Invalid self-host entitlement signing configuration"); }
}

export function createSelfHostIssuer(config: SelfHostSigningConfig) {
  return async (userId: string, eligibleUntil: Date, now: Date) => {
    const issuedAt = Math.floor(now.getTime() / 1000);
    const expiresAt = Math.min(Math.floor(eligibleUntil.getTime() / 1000), issuedAt + maximumLifetimeSeconds);
    if (!Number.isSafeInteger(issuedAt) || !Number.isSafeInteger(expiresAt) || expiresAt <= issuedAt) throw new Error("Entitlement has expired");
    const subject = "license_" + createHmac("sha256", config.subjectSecret).update(userId).digest("base64url");
    const token = await new SignJWT({ sub: subject, status: "eligible", schema_version: 1 })
      .setProtectedHeader({ alg: "EdDSA", kid: config.keyId, typ: "JWT" })
      .setIssuer("misty-hosted").setAudience("misty-self-hosted").setIssuedAt(issuedAt).setExpirationTime(expiresAt)
      .setJti(`entitlement_${randomUUID()}`).sign(config.privateKey);
    return { token, expires_at: new Date(expiresAt * 1000).toISOString() };
  };
}
