// Copyright 2026 CiCy AI
// SPDX-License-Identifier: Apache-2.0

import apiService from './api';

export const sendCommandToTmux = async (
  command: string,
  tmuxTarget: string,
  submit = true,
  options?: { deferUntilReady?: boolean },
): Promise<{ success: boolean; message: string }> => {
  console.log('[sendCommandToTmux] currentPaneId (tmuxTarget):', tmuxTarget, 'command:', command, 'submit:', submit);
  const { data } = await apiService.sendCommand(tmuxTarget, command, submit, options);
  return { success: data.success, message: data.success ? 'Sent to tmux' : data.detail };
};
