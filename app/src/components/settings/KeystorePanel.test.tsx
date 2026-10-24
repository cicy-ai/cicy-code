import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { beforeEach, describe, expect, it, vi } from 'vitest';

vi.mock('react-i18next', () => ({
  useTranslation: () => ({
    t: (key: string, opts?: any) => (typeof opts === 'object' && opts?.defaultValue) || key,
  }),
}));

const api = vi.hoisted(() => ({
  listKeystore: vi.fn(),
  saveKeystoreKey: vi.fn(),
  deleteKeystoreKey: vi.fn(),
}));
vi.mock('../../services/api', () => ({ default: api }));

import KeystorePanel from './KeystorePanel';

const listed = [{ key: 'OPENAI_KEY', note: 'OpenAI prod', has_value: true, updated_at: '2026-10-24T00:00:00Z' }];

beforeEach(() => {
  vi.clearAllMocks();
  api.listKeystore.mockResolvedValue({ data: { keys: listed } });
  api.saveKeystoreKey.mockResolvedValue({ data: { keys: listed } });
});

describe('<KeystorePanel />', () => {
  it('lists keys with their note and set state', async () => {
    render(<KeystorePanel active />);
    expect(await screen.findByText('OPENAI_KEY')).toBeInTheDocument();
    expect(screen.getByText('OpenAI prod')).toBeInTheDocument();
    expect(screen.getByText('已设置')).toBeInTheDocument();
  });

  it('creates a key with value and note, then clears the form', async () => {
    api.listKeystore.mockResolvedValue({ data: { keys: [] } });
    render(<KeystorePanel active />);
    fireEvent.click(await screen.findByRole('button', { name: /新增/ }));
    const save = document.querySelector('[data-id="keystore-form-save"]') as HTMLButtonElement;
    fireEvent.change(document.querySelector('[data-id="keystore-form-key"]')!, { target: { value: 'GH_TOKEN' } });
    expect(save).toBeDisabled(); // new key needs a value
    const value = document.querySelector('[data-id="keystore-form-value"]') as HTMLInputElement;
    expect(value.type).toBe('password');
    fireEvent.change(value, { target: { value: 'ghp_secret' } });
    fireEvent.change(document.querySelector('[data-id="keystore-form-note"]')!, { target: { value: 'GitHub bot' } });
    fireEvent.click(save);
    await waitFor(() => expect(api.saveKeystoreKey).toHaveBeenCalledWith({ key: 'GH_TOKEN', value: 'ghp_secret', note: 'GitHub bot' }));
    await waitFor(() => expect(document.querySelector('[data-id="keystore-form"]')).not.toBeInTheDocument());
  });

  it('editing with an empty value keeps the stored one (value not sent)', async () => {
    render(<KeystorePanel active />);
    fireEvent.click(await screen.findByLabelText('编辑'));
    const value = document.querySelector('[data-id="keystore-form-value"]') as HTMLInputElement;
    expect(value.value).toBe(''); // the server never sends the value back
    fireEvent.change(document.querySelector('[data-id="keystore-form-note"]')!, { target: { value: 'new note' } });
    fireEvent.click(document.querySelector('[data-id="keystore-form-save"]')!);
    await waitFor(() => expect(api.saveKeystoreKey).toHaveBeenCalledWith({ key: 'OPENAI_KEY', old_key: 'OPENAI_KEY', note: 'new note' }));
  });

  it('rejects key names that are not env-var safe', async () => {
    api.listKeystore.mockResolvedValue({ data: { keys: [] } });
    render(<KeystorePanel active />);
    fireEvent.click(await screen.findByRole('button', { name: /新增/ }));
    fireEvent.change(document.querySelector('[data-id="keystore-form-key"]')!, { target: { value: '1-bad' } });
    fireEvent.change(document.querySelector('[data-id="keystore-form-value"]')!, { target: { value: 'x' } });
    expect(document.querySelector('[data-id="keystore-form-save"]')).toBeDisabled();
  });
});
