import apiService from './api';

// 发送命令到 tmux
export const sendCommandToTmux = async (command: string, tmuxTarget: string): Promise<{ success: boolean; message: string }> => {
  console.log('[sendCommandToTmux] currentPaneId (tmuxTarget):', tmuxTarget, 'command:', command);
  const { data } = await apiService.sendCommand(tmuxTarget, command);
  return { success: data.success, message: data.success ? 'Sent to tmux' : data.detail };
};

// 转发快捷键
export const sendShortcut = async (key: string, target?: string): Promise<void> => {
  await apiService.sendKeys(target || '', key).catch(() => {});
};
