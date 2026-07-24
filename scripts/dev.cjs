const { spawn } = require("node:child_process");

const backend = spawn("go", ["run", "./backend/cmd/trex"], {
  stdio: "inherit",
  shell: true,
  env: { ...process.env, TREX_DEV: "1" }
});

const timer = setTimeout(() => {
  const electron = spawn("node_modules\\.bin\\electron.cmd", ["."], {
    stdio: "inherit",
    shell: true,
    env: { ...process.env, TREX_BACKEND_URL: "http://127.0.0.1:8787" }
  });
  electron.on("exit", code => {
    backend.kill();
    process.exit(code ?? 0);
  });
}, 1100);

backend.on("exit", code => {
  clearTimeout(timer);
  process.exit(code ?? 1);
});
