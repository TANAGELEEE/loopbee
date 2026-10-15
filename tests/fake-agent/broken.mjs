#!/usr/bin/env node
// An agent CLI whose installation is broken: macOS kills it as soon as it starts (seen with OpenCode, exit 137).
process.kill(process.pid, "SIGKILL");
