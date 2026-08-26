import { describe, expect, it } from 'vitest';
import { classifySystemNotice } from './systemNotice';

describe('classifySystemNotice', () => {
  it('drops pure harness noise', () => {
    expect(classifySystemNotice('<total_tokens>14999856 tokens left</total_tokens>')).toBeNull();
    expect(classifySystemNotice("Only you see that command's output — the user's terminal shows at most a few lines of it. If the user needs to read any of it, put it in your reply.\n\n<total_tokens>1 tokens left</total_tokens>")).toBeNull();
    expect(classifySystemNotice("The user hasn't heard from you in a while — say in a few words what you're doing, then continue.")).toBeNull();
    expect(classifySystemNotice('<system-reminder>\n<total_tokens>5 tokens left</total_tokens>\n</system-reminder>')).toBeNull();
  });

  it('extracts a mid-turn user message as a steer', () => {
    const n = classifySystemNotice("The user sent a new message while you were working:\n分身加到project 会与其他的agent card 重叠\n\n第二段\n\nThis is how Claude Code surfaces messages the user sends mid-turn — within the running turn, often alongside the next tool result, rather than as a separate conversation turn. Address the message above as you continue this turn.\n\n<total_tokens>1 tokens left</total_tokens>");
    expect(n?.kind).toBe('steer');
    expect(n?.text).toBe('分身加到project 会与其他的agent card 重叠\n\n第二段');
  });

  it('turns a task notification into a task notice with its summary', () => {
    const n = classifySystemNotice('[SYSTEM NOTIFICATION - NOT USER INPUT]\nThis is an automated background-task event.\n\n<task-notification>\n<task-id>bltct1320</task-id>\n<status>failed</status>\n<summary>Background command "Download Go" failed with exit code 28</summary>\n</task-notification>');
    expect(n?.kind).toBe('task');
    expect(n?.text).toBe('Background command "Download Go" failed with exit code 28');
    expect(n?.title).toBe('failed · bltct1320');
  });

  it('classifies injected context and keeps other notices', () => {
    expect(classifySystemNotice("As you answer the user's questions, you can use the following context:\n# claudeMd\nCodebase and user instructions are shown below.")?.kind).toBe('context');
    const n = classifySystemNotice('Some other harness instruction.\n\n<total_tokens>1 tokens left</total_tokens>');
    expect(n?.kind).toBe('notice');
    expect(n?.text).toBe('Some other harness instruction.');
  });
});
