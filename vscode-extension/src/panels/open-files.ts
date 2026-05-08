import * as vscode from 'vscode';
import * as path from 'path';

interface OpenFilePanel {
  panel: vscode.WebviewPanel | undefined;
  context: vscode.ExtensionContext | undefined;
  readonly: boolean;
}

const openFilesPanel: OpenFilePanel = {
  panel: undefined,
  context: undefined,
  readonly: true,
};

export function registerOpenFilesPanel(context: vscode.ExtensionContext) {
  openFilesPanel.context = context;

  // Watch for file changes and tab changes
  const tabChanges = vscode.workspace.onDidChangeTextDocument(() => {
    updateOpenFilesPanel();
  });

  const activeEditorChanges = vscode.window.onDidChangeActiveTextEditor(() => {
    updateOpenFilesPanel();
  });

  context.subscriptions.push(tabChanges, activeEditorChanges);

  // Command to open the panel
  const openPanel = vscode.commands.registerCommand('cairo.openFilesPanel', () => {
    if (!openFilesPanel.panel) {
      openFilesPanel.panel = vscode.window.createWebviewPanel(
        'cairoOpenFiles',
        'Open Files',
        vscode.ViewColumn.Beside,
        {
          enableScripts: true,
          retainContextWhenHidden: true,
        }
      );

      openFilesPanel.panel.onDidDispose(() => {
        openFilesPanel.panel = undefined;
      }, null, context.subscriptions);

      openFilesPanel.panel.webview.html = getOpenFilesWebviewContent(
        openFilesPanel.panel.webview
      );

      openFilesPanel.panel.webview.onDidReceiveMessage(
        (message) => {
          handlePanelMessage(message);
        },
        null,
        context.subscriptions
      );
    }

    openFilesPanel.panel.reveal(vscode.ViewColumn.Beside);
  });

  context.subscriptions.push(openPanel);
}

function updateOpenFilesPanel() {
  if (!openFilesPanel.panel) return;

  const openFiles = vscode.window.tabGroups.all.flatMap((group) =>
    group.tabs.map((tab) => {
      if (tab.input instanceof vscode.TabInputText) {
        return tab.input.uri;
      }
      return null;
    })
  ).filter((uri): uri is vscode.Uri => uri !== null);

  const activeUri = vscode.window.activeTextEditor?.document.uri;

  const message = {
    type: 'update-files',
    files: openFiles.map((uri) => ({
      path: uri.fsPath,
      name: path.basename(uri.fsPath),
      isActive: uri.toString() === activeUri?.toString(),
    })),
  };

  if (openFilesPanel.panel.webview) {
    openFilesPanel.panel.webview.postMessage(message);
  }
}

function handlePanelMessage(message: { type: string; fileName: string; action: string }) {
  switch (message.type) {
    case 'action':
      const uri = vscode.Uri.file(message.fileName);
      if (message.action === 'diff') {
        vscode.commands.executeCommand('workbench.actions.view.scm');
      } else if (message.action === 'view') {
        vscode.window.showTextDocument(uri);
      } else if (message.action === 'copy') {
        vscode.env.clipboard.writeText(message.fileName);
        vscode.window.showInformationMessage(`Path copied: ${message.fileName}`);
      }
      break;
  }
}

function getOpenFilesWebviewContent(webview: vscode.Webview): string {
  const nonce = getNonce();

  return `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1.0">
  <title>Open Files</title>
  <style>
    body {
      font-family: 'Consolas', 'Courier New', monospace;
      background: #1e1e1e;
      color: #cccccc;
      margin: 0;
      padding: 10px;
      font-size: 13px;
    }

    .file-item {
      padding: 8px 12px;
      border-bottom: 1px solid #3c3c3c;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }

    .file-item:last-child {
      border-bottom: none;
    }

    .file-item.active {
      background: #2d333b;
      border-left: 3px solid #4ec9b0;
    }

    .file-name {
      flex: 1;
      overflow: hidden;
      text-overflow: ellipsis;
      white-space: nowrap;
      cursor: pointer;
      padding-right: 8px;
    }

    .file-name:hover {
      color: #4ec9b0;
    }

    .file-actions {
      display: flex;
      gap: 4px;
    }

    .file-btn {
      padding: 4px 8px;
      background: #3c3c3c;
      color: #cccccc;
      border: 1px solid #3c3c3c;
      border-radius: 3px;
      cursor: pointer;
      font-size: 11px;
      min-width: 32px;
    }

    .file-btn:hover {
      background: #4c4c4c;
      border-color: #4ec9b0;
    }

    .no-files {
      text-align: center;
      padding: 20px;
      color: #888;
      font-size: 12px;
    }

    h3 {
      font-size: 12px;
      margin: 0 0 10px 0;
      color: #cccccc;
      text-transform: uppercase;
      letter-spacing: 1px;
    }
  </style>
</head>
<body>
  <h3>Open Files</h3>
  <div id="file-list"></div>

  <script>
    const vscode = acquireVsCodeApi();
    const fileList = document.getElementById('file-list');

    window.addEventListener('message', function(event) {
      const message = event.data;
      if (message.type === 'update-files') {
        renderFiles(message.files);
      }
    });

    function renderFiles(files) {
      if (files.length === 0) {
        fileList.innerHTML = '<div class="no-files">No open files</div>';
        return;
      }

      const html = files.map(function(file) {
        const isActive = file.isActive ? 'active' : '';
        const escapedPath = file.path.replace(/'/g, "\\'");
        const escapedName = file.name.replace(/'/g, "\\'");
        
        return '<div class="file-item ' + isActive + '">' +
          '<div class="file-name" onclick="vscode.postMessage({type: ' + "'" + 'action' + "'" + ', action: ' + "'" + 'view' + "'" + ', fileName: ' + "'" + escapedPath + "'" + ')">' +
          escapedName +
          '</div>' +
          '<div class="file-actions">' +
          '<button class="file-btn" onclick="vscode.postMessage({type: ' + "'" + 'action' + "'" + ', action: ' + "'" + 'diff' + "'" + ', fileName: ' + "'" + escapedPath + "'" + ')">Diff</button>' +
          '<button class="file-btn" onclick="vscode.postMessage({type: ' + "'" + 'action' + "'" + ', action: ' + "'" + 'copy' + "'" + ', fileName: ' + "'" + escapedPath + "'" + ')">Copy</button>' +
          '</div>' +
          '</div>';
      }).join('');
      
      fileList.innerHTML = html;
    }
  </script>
</body>
</html>`;
}

function getNonce(): string {
  let text = '';
  const possible = 'ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789';
  for (let i = 0; i < 32; i++) {
    text += possible.charAt(Math.floor(Math.random() * possible.length));
  }
  return text;
}
