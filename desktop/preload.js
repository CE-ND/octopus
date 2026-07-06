const { ipcRenderer } = require('electron');

const styleId = 'octopus-window-controls-style';
const controlsId = 'octopus-window-controls';
const hotCornerId = 'octopus-window-hot-corner';
const dragStripId = 'octopus-window-drag-strip';

function sendWindowAction(action) {
  ipcRenderer.send(`octopus-window:${action}`);
}

function createWindowButton(action, title) {
  const button = document.createElement('button');
  button.type = 'button';
  button.className = `octopus-window-control octopus-window-control-${action}`;
  button.title = title;
  button.setAttribute('aria-label', title);

  const icon = document.createElement('span');
  icon.className = `octopus-window-control-icon octopus-window-control-icon-${action}`;
  button.appendChild(icon);

  button.addEventListener('click', (event) => {
    event.preventDefault();
    event.stopPropagation();
    sendWindowAction(action);
  });
  return button;
}

function ensureDesktopWindowControls() {
  if (!document.body || document.getElementById(controlsId)) return;

  const style = document.createElement('style');
  style.id = styleId;
  style.textContent = `
    #${dragStripId} {
      position: fixed;
      top: 0;
      left: 0;
      right: 176px;
      height: 10px;
      z-index: 2147483644;
      -webkit-app-region: drag;
    }

    #${hotCornerId} {
      position: fixed;
      top: 0;
      right: 0;
      width: 176px;
      height: 76px;
      z-index: 2147483646;
      -webkit-app-region: no-drag;
    }

    #${controlsId} {
      position: fixed;
      top: 22px;
      right: 24px;
      z-index: 2147483647;
      display: flex;
      align-items: center;
      gap: 9px;
      padding: 0;
      border-radius: 0;
      background: transparent;
      box-shadow: none;
      opacity: 0;
      pointer-events: none;
      transform: translateY(-6px);
      transition: opacity 140ms ease, transform 140ms ease;
      user-select: none;
      -webkit-app-region: no-drag;
    }

    #${hotCornerId}:hover ~ #${controlsId},
    #${controlsId}:hover,
    #${controlsId}:focus-within {
      opacity: 1;
      pointer-events: auto;
      transform: translateY(0);
    }

    .octopus-window-control {
      width: 32px;
      height: 32px;
      position: relative;
      display: inline-grid;
      place-items: center;
      border: 1px solid rgba(255, 255, 255, 0.12);
      border-radius: 999px;
      background: rgba(255, 255, 255, 0.08);
      color: rgba(255, 255, 255, 0.76);
      cursor: default;
      padding: 0;
      backdrop-filter: blur(10px);
      transition: background 120ms ease, border-color 120ms ease, color 120ms ease;
    }

    .octopus-window-control:hover {
      border-color: rgba(255, 255, 255, 0.24);
      background: rgba(255, 255, 255, 0.16);
      color: #ffffff;
    }

    .octopus-window-control-icon {
      position: relative;
      display: block;
      width: 14px;
      height: 14px;
      color: currentColor;
      pointer-events: none;
    }

    .octopus-window-control-icon-minimize::before {
      content: "";
      position: absolute;
      left: 2px;
      right: 2px;
      top: 6px;
      height: 1.5px;
      border-radius: 999px;
      background: currentColor;
    }

    .octopus-window-control-icon-toggle-maximize::before {
      content: "";
      position: absolute;
      left: 3px;
      top: 3px;
      width: 8px;
      height: 8px;
      border: 1.5px solid currentColor;
      border-radius: 2px;
      box-sizing: border-box;
    }

    .octopus-window-control-icon-close::before,
    .octopus-window-control-icon-close::after {
      content: "";
      position: absolute;
      left: 2px;
      top: 6px;
      width: 10px;
      height: 1.5px;
      border-radius: 999px;
      background: currentColor;
      transform-origin: center;
    }

    .octopus-window-control-icon-close::before {
      transform: rotate(45deg);
    }

    .octopus-window-control-icon-close::after {
      transform: rotate(-45deg);
    }

    .octopus-window-control-close:hover {
      border-color: rgba(232, 17, 35, 0.48);
      background: rgba(232, 17, 35, 0.72);
      color: #ffffff;
    }
  `;

  const dragStrip = document.createElement('div');
  dragStrip.id = dragStripId;

  const hotCorner = document.createElement('div');
  hotCorner.id = hotCornerId;

  const controls = document.createElement('div');
  controls.id = controlsId;
  controls.append(
    createWindowButton('minimize', 'Minimize'),
    createWindowButton('toggle-maximize', 'Maximize'),
    createWindowButton('close', 'Close')
  );

  document.head.appendChild(style);
  document.body.append(dragStrip, hotCorner, controls);
}

if (document.readyState === 'loading') {
  document.addEventListener('DOMContentLoaded', ensureDesktopWindowControls, { once: true });
} else {
  ensureDesktopWindowControls();
}
