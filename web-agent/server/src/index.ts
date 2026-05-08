import { createApp } from './createApp.js';
import { readConfig } from './config.js';

async function main() {
  const config = readConfig();

  if (typeof process.getuid === 'function' && process.getuid() === 0) {
    console.warn('cairo-web: warning: running as root is not recommended');
  }

  const app = createApp({ config });
  await app.listen({ host: config.host, port: config.port });
  console.log(`cairo-web: listening on http://${config.host}:${config.port}`);
  console.log(`cairo-web: workspace roots: ${config.workspaceRoots.join(', ')}`);
  if (config.authRequired) console.log('cairo-web: bearer token auth enabled');
}

main().catch((err) => {
  console.error(`cairo-web: ${err instanceof Error ? err.message : String(err)}`);
  process.exit(1);
});
