import apiService from './api';

export const sendCommandToTmux = async (command: string, tmuxTarget: string, submit = true): Promise<{ success: boolean; message: string }> => {
  console.log('[sendCommandToTmux] currentPaneId (tmuxTarget):', tmuxTarget, 'command:', command, 'submit:', submit);
  const { data } = await apiService.sendCommand(tmuxTarget, command, submit);
  return { success: data.success, message: data.success ? 'Sent to tmux' : data.detail };
};

export const sendShortcut = async (key: string, target?: string): Promise<void> => {
  await apiService.sendKeys(target || '', key).catch(() => {});
};
