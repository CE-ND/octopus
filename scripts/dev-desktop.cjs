#!/usr/bin/env node
'use strict';

/**
 * Octopus 开发桌面端启动器。
 *
 * 与生产桌面端并存：
 * - 生产：%USERPROFILE%\.octopus，18777，Electron 默认 userData
 * - 开发：%USERPROFILE%\.octopus-dev，18778，独立 Electron userData
 * - 开发配置：%USERPROFILE%\.octopus-dev\data\config.json（后端首次启动生成）
 *
 * 用法：
 *   pnpm dev
 *   pnpm dev -- --no-build
 *   pnpm dev -- --port=18779
 */

const { spawn } = require('child_process');
const fs = require('fs');
const net = require('net');
const os = require('os');
const path = require('path');

const repoRoot = path.resolve(__dirname, '..');
const devHome = path.join(os.homedir(), '.octopus-dev');
const devData = path.join(devHome, 'data');
const devPlugins = path.join(devData, 'plugins');
const devLogs = path.join(devHome, 'logs');
const electronProfile = path.join(devHome, 'electron-profile');
const configPath = path.join(devData, 'config.json');

function parseArgs(argv) {
  const options = {
    noBuild: false,
    port: Number(process.env.OCTOPUS_DEV_PORT || 18778),
  };
  for (const arg of argv) {
    if (arg === '--no-build' || arg === '-NoBuild') {
      options.noBuild = true;
      continue;
    }
    if (arg.startsWith('--port=')) {
      const port = Number(arg.slice('--port='.length));
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        throw new Error(`无效端口: ${arg}`);
      }
      options.port = port;
    }
  }
  return options;
}

function isPortAvailable(port) {
  return new Promise((resolve) => {
    const server = net.createServer();
    server.once('error', () => resolve(false));
    server.listen({ host: '127.0.0.1', port }, () => {
      server.close(() => resolve(true));
    });
  });
}

function runPrepare() {
  return new Promise((resolve, reject) => {
    const env = { ...process.env };
    if (fs.existsSync(path.join(repoRoot, 'web', 'node_modules'))) {
      // node_modules 已存在时跳过耗时的 pnpm install。
      env.OCTOPUS_DESKTOP_SKIP_INSTALL = '1';
    }

    console.log('[dev] 构建开发桌面资源（前端静态文件 + Go 后端）...');
    const child = spawn('pnpm', ['desktop:prepare'], {
      cwd: repoRoot,
      env,
      stdio: 'inherit',
      shell: process.platform === 'win32',
    });
    child.on('error', reject);
    child.on('exit', (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`pnpm desktop:prepare 失败（退出码 ${code}）`));
      }
    });
  });
}

async function main() {
  const options = parseArgs(process.argv.slice(2));

  fs.mkdirSync(devData, { recursive: true });
  fs.mkdirSync(devPlugins, { recursive: true });
  fs.mkdirSync(devLogs, { recursive: true });
  fs.mkdirSync(electronProfile, { recursive: true });
  // config.json 由后端 conf.Load 首次启动时按统一默认模板生成，启动器不再预写。
  if (!fs.existsSync(configPath)) {
    console.log(`[dev] 首次启动将由后端生成默认配置: ${configPath}`);
  }

  if (!(await isPortAvailable(options.port))) {
    throw new Error(
      `开发端口 ${options.port} 已被占用。请先关闭占用进程，或使用 --port=其他端口。`
    );
  }

  if (!(await isPortAvailable(18777))) {
    console.log('[dev] 检测到生产桌面端正在 18777 运行，开发环境不会影响它。');
  }

  if (!options.noBuild) {
    await runPrepare();
  }

  const electronBinary = require('electron');
  const backendBinary = path.join(repoRoot, 'build', 'desktop', 'backend', process.platform === 'win32' ? 'octopus.exe' : 'octopus');
  if (!fs.existsSync(backendBinary)) {
    throw new Error(`未找到开发后端: ${backendBinary}，请先执行 pnpm desktop:prepare`);
  }

  const env = {
    ...process.env,
    OCTOPUS_DESKTOP_PORT: String(options.port),
    OCTOPUS_DESKTOP_DATA_DIR: devHome,
  };
  // 某些终端/IDE 会设置 ELECTRON_RUN_AS_NODE，导致 Electron 以 Node 模式启动。
  delete env.ELECTRON_RUN_AS_NODE;

  console.log(`[dev] 启动开发桌面端：端口=${options.port}`);
  console.log(`[dev] 数据目录: ${devHome}`);
  console.log(`[dev] Electron profile: ${electronProfile}`);

  const child = spawn(electronBinary, [repoRoot, `--user-data-dir=${electronProfile}`], {
    cwd: repoRoot,
    env,
    stdio: 'inherit',
  });
  child.on('error', (err) => {
    console.error(`[dev] 启动 Electron 失败: ${err.message}`);
    process.exitCode = 1;
  });
  child.on('exit', (code) => {
    process.exitCode = code == null ? 0 : code;
  });
}

main().catch((err) => {
  console.error(`[dev] ${err instanceof Error ? err.message : err}`);
  process.exitCode = 1;
});
