# Standard sandbox sizes

`standard-sizes.json` is the single source of truth for the default Standard size of each sandbox on a self-hosted backend. The console setup wizard offers it as Standard and derives Small (half) and Large (double) from it.

## Structure and units

```json
{
  "docker": { "cpus": 2, "memory_mib": 2048 },
  "microsandbox": { "cpus": 2, "memory_mib": 4096, "root_disk_mib": 8192, "environment_disk_mib": 8192 }
}
```

- `cpus`: whole CPUs, a positive integer.
- `memory_mib`, `root_disk_mib`, `environment_disk_mib`: MiB, positive integers.
- `docker` has exactly `cpus` and `memory_mib`; it has no disk fields.
- `microsandbox` has exactly all four fields.

The values must stay within the bounds that Core and `validSandboxResources` accept.

## Readers

The Web setup wizard is the only reader, through `defaultSandboxResources` in `deployment-specification.ts`. `deployment-specification.test.ts` pins the structure so that an accidental change fails.
