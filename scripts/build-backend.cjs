const { spawnSync } = require("node:child_process");
const { mkdirSync } = require("node:fs");
const { join } = require("node:path");

mkdirSync(join(process.cwd(), "bin"), { recursive: true });
const environment = {
  ...process.env,
  GOCACHE: join(process.cwd(), ".cache", "go-build"),
  GOMODCACHE: join(process.cwd(), ".cache", "go-mod")
};
const result = spawnSync(
  "go",
  ["build", "-buildvcs=false", "-trimpath", "-ldflags=-s -w -H=windowsgui", "-o", "bin/trex-backend.exe", "./backend/cmd/trex"],
  { stdio: "inherit", shell: false, env: environment }
);
process.exit(result.status ?? 1);
