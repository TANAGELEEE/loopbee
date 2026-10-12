// Fake GitHub CLI for UI tests: installed and signed in, nothing else.
const args = process.argv.slice(2);
if (args.includes("--version")) console.log("gh version 2.99.0 (fake)");
else if (args[0] === "auth" && args[1] === "status") process.exit(0);
else {
  console.error("fake gh: unsupported " + args.join(" "));
  process.exit(1);
}
