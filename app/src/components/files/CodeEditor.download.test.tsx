import { describe, expect, it, vi, beforeEach } from 'vitest';
import { fireEvent, render, screen, waitFor } from '@testing-library/react';

vi.mock('./api', async (importOriginal) => {
  const actual = await importOriginal<typeof import('./api')>();
  return {
    ...actual,
    fsApi: {
      ...actual.fsApi,
      stat: vi.fn(),
      read: vi.fn(),
      download: vi.fn(),
    },
  };
});

import CodeEditor from './CodeEditor';
import { fsApi } from './api';

describe('CodeEditor binary download', () => {
  beforeEach(() => {
    vi.mocked(fsApi.stat).mockResolvedValue({
      name: 'Luckgram-intro-en.pdf',
      path: 'assets/2026/11/06/Luckgram-intro-en.pdf',
      is_dir: false,
      size: 309230,
      mtime: 0,
      mode: '',
      mime: 'application/pdf',
    });
    vi.mocked(fsApi.download).mockClear();
  });

  it('downloads from the root the file was opened under, not the workspace', async () => {
    render(
      <CodeEditor agentId="w-1002" path="assets/2026/11/06/Luckgram-intro-en.pdf" root="cicy-ai" />,
    );
    const button = await waitFor(() => {
      const el = document.querySelector('[data-id="code-editor-binary-download"]');
      if (!el) throw new Error('download button not rendered yet');
      return el as HTMLElement;
    });
    fireEvent.click(button);
    expect(fsApi.download).toHaveBeenCalledWith('w-1002', 'assets/2026/11/06/Luckgram-intro-en.pdf', 'cicy-ai');
    expect(screen.getByText('Luckgram-intro-en.pdf')).toBeTruthy();
  });
});
