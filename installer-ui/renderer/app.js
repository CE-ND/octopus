const pages = [...document.querySelectorAll('.page')];
const steps = [...document.querySelectorAll('.step-list li')];
const backButton = document.getElementById('backButton');
const nextButton = document.getElementById('nextButton');
const minimizeButton = document.getElementById('minimizeButton');
const closeButton = document.getElementById('closeButton');
const titlebarStatus = document.getElementById('titlebarStatus');
const installDirInput = document.getElementById('installDir');
const browseButton = document.getElementById('browseButton');
const desktopShortcutInput = document.getElementById('desktopShortcut');
const scopeNote = document.getElementById('scopeNote');
const dataDirLabel = document.getElementById('dataDirLabel');
const dataPathReview = document.getElementById('dataPathReview');
const payloadLabel = document.getElementById('payloadLabel');
const payloadItem = payloadLabel.closest('.check-item');
const installStateLabel = document.getElementById('installStateLabel');
const installTitle = document.getElementById('installTitle');
const installMessage = document.getElementById('installMessage');
const installingPanel = document.getElementById('installingPanel');
const progressBar = document.getElementById('progressBar');
const footerMeta = document.getElementById('footerMeta');
const segments = [...document.querySelectorAll('.segment')];

const pageLabels = ['准备', '选项', '检查', '安装'];
const pageMeta = [
  '确认本机服务入口和数据目录。',
  '选择安装范围、程序位置和桌面快捷方式。',
  '确认 payload 与安装计划。',
  '安装期间请保持窗口打开。',
];

const state = {
  page: 0,
  scope: 'currentUser',
  defaults: null,
  installResult: null,
  installing: false,
  complete: false,
  failed: false,
};

function setInstallVisual(status) {
  installingPanel.classList.toggle('is-installing', status === 'installing');
  installingPanel.classList.toggle('is-complete', status === 'complete');
  installingPanel.classList.toggle('is-error', status === 'error');
}

function setPage(page) {
  state.page = page;
  pages.forEach((node, index) => node.classList.toggle('active', index === page));
  steps.forEach((node, index) => {
    const isCurrent = index === page;
    const isComplete = index < page || (state.complete && index === page);
    node.classList.toggle('active', index <= page);
    node.classList.toggle('current', isCurrent);
    node.classList.toggle('complete', isComplete);
    node.setAttribute('aria-current', isCurrent ? 'step' : 'false');
  });

  titlebarStatus.textContent = pageLabels[page];
  footerMeta.textContent = state.complete
    ? '安装完成，可以启动桌面端。'
    : state.failed
      ? '安装未完成，请重试或返回修改选项。'
      : page === 2 && state.defaults && !state.defaults.payloadExists
        ? '缺少安装 payload，无法继续。'
        : pageMeta[page];

  backButton.disabled = page === 0 || state.installing;
  backButton.style.visibility = page === 0 || state.complete ? 'hidden' : 'visible';

  if (state.complete) {
    nextButton.textContent = '启动 Octopus';
  } else if (state.failed && page === 3) {
    nextButton.textContent = '重试';
  } else if (page === 2) {
    nextButton.textContent = '开始安装';
  } else if (page === 3) {
    nextButton.textContent = '安装中';
  } else {
    nextButton.textContent = '继续';
  }

  nextButton.disabled = state.installing || (page === 2 && state.defaults && !state.defaults.payloadExists);
}

function setScope(scope) {
  state.scope = scope;
  segments.forEach((segment) => {
    const active = segment.dataset.scope === scope;
    segment.classList.toggle('active', active);
    segment.setAttribute('aria-checked', active ? 'true' : 'false');
  });

  if (state.defaults) {
    installDirInput.value = scope === 'allUsers' ? state.defaults.allUsersDir : state.defaults.currentUserDir;
  }

  scopeNote.textContent =
    scope === 'allUsers'
      ? '会为这台电脑上的所有用户安装，Windows 可能会请求授权。'
      : '仅写入当前用户的应用目录，不需要管理员权限。';
}

async function install() {
  state.installing = true;
  state.failed = false;
  setPage(3);
  setInstallVisual('installing');
  installStateLabel.textContent = '正在安装';
  installTitle.textContent = '写入 Octopus Desktop';
  installMessage.textContent = '这通常只需要一点时间。请保持此窗口打开。';
  progressBar.style.animation = '';

  try {
    state.installResult = await window.octopusSetup.install({
      scope: state.scope,
      installDir: installDirInput.value.trim(),
      desktopShortcut: desktopShortcutInput.checked,
    });

    state.installing = false;
    state.complete = true;
    state.failed = false;
    setInstallVisual('complete');
    installStateLabel.textContent = '安装完成';
    installTitle.textContent = 'Octopus 已准备就绪';
    installMessage.textContent = '可以立即启动桌面端，进入管理面板继续配置。';
    progressBar.style.animation = 'none';
    progressBar.style.transform = 'none';
    progressBar.style.width = '100%';
    setPage(3);
  } catch (error) {
    state.installing = false;
    state.failed = true;
    setInstallVisual('error');
    installStateLabel.textContent = '安装失败';
    installTitle.textContent = '没有完成安装';
    installMessage.textContent = error instanceof Error ? error.message : String(error);
    nextButton.textContent = '重试';
    nextButton.disabled = false;
    backButton.disabled = false;
  }
}

async function init() {
  minimizeButton.addEventListener('click', () => window.octopusSetup.minimize());
  closeButton.addEventListener('click', () => window.octopusSetup.close());

  state.defaults = await window.octopusSetup.getDefaults();
  dataDirLabel.textContent = state.defaults.dataDir;
  dataPathReview.textContent = `配置、数据库和日志会保存在 ${state.defaults.dataDir}`;
  payloadLabel.textContent = state.defaults.payloadExists ? '已找到 Octopus Desktop 安装 payload' : '没有找到安装 payload';
  payloadItem.classList.toggle('error', !state.defaults.payloadExists);
  setScope('currentUser');

  segments.forEach((segment) => {
    segment.addEventListener('click', () => setScope(segment.dataset.scope));
  });

  browseButton.addEventListener('click', async () => {
    const selected = await window.octopusSetup.selectDirectory(installDirInput.value);
    if (selected) {
      installDirInput.value = selected;
    }
  });

  backButton.addEventListener('click', () => {
    if (state.page > 0) {
      setPage(state.page - 1);
    }
  });

  nextButton.addEventListener('click', async () => {
    if (state.complete) {
      await window.octopusSetup.launch(state.installResult?.executable);
      window.octopusSetup.close();
      return;
    }

    if (state.page === 2 || state.page === 3) {
      await install();
      return;
    }

    setPage(state.page + 1);
  });

  window.octopusSetup.onInstallLog((event) => {
    if (event?.message) {
      installMessage.textContent = event.message;
    }
  });

  setPage(0);
}

init().catch((error) => {
  setInstallVisual('error');
  installMessage.textContent = error instanceof Error ? error.message : String(error);
});
