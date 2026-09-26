UPDATE public.trusted_devices
SET platform = 'unknown'
WHERE platform NOT IN ('macos', 'windows', 'linux', 'unknown');

ALTER TABLE public.trusted_devices DROP CONSTRAINT trusted_devices_platform_check;
ALTER TABLE public.trusted_devices ADD CONSTRAINT trusted_devices_platform_check
    CHECK (platform IN ('macos', 'windows', 'linux', 'unknown'));
