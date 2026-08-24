import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (_key: string, options?: { defaultValue?: string }) => options?.defaultValue || _key,
  }),
}));

const api = vi.hoisted(() => ({
  importCodexAuth: vi.fn(),
}));

vi.mock('../../services/api', () => ({ default: api }));

import CodexAuthImportCard from './CodexAuthImportCard';

beforeEach(() => {
  vi.clearAllMocks();
});

describe('<CodexAuthImportCard />', () => {
  it('confirms before restoring pasted Base64 and clears the credential after success', async () => {
    const encoded = 'eyJhdXRoX21vZGUiOiJjaGF0Z3B0In0=';
    api.importCodexAuth.mockImplementation((value: string) => {
      if (value !== encoded) return Promise.reject(new Error('wrong credential'));
      return Promise.resolve({ data: { success: true } });
    });

    render(<CodexAuthImportCard />);

    const input = screen.getByLabelText('Codex Auth Base64');
    expect(input).toHaveAttribute('type', 'password');
    fireEvent.change(input, { target: { value: encoded } });
    fireEvent.click(screen.getByRole('button', { name: '还原并覆盖' }));

    expect(await screen.findByText('覆盖 Codex Auth？')).toBeInTheDocument();
    fireEvent.click(document.querySelector('[data-id="modal-confirm"]') as HTMLButtonElement);

    await waitFor(() => expect(screen.getByText('已覆盖，之后启动的 Codex 会使用新凭据。')).toBeInTheDocument());
    expect(input).toHaveValue('');
  });
});

