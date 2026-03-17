/**
 * Token Management Utility
 * Handles token extraction from URL, storage, and cleanup
 */

const TOKEN_STORAGE_KEY = 'api_token';

export class TokenManager {
  /**
   * Initialize token from URL or localStorage
   * @returns token string or null
   */
  static init(): string | null {
    // Clean up legacy token keys
    this.cleanupLegacyTokens();

    // 1. Check URL for token parameter
    const urlToken = this.extractTokenFromUrl();
    
    if (urlToken) {
      // Save to localStorage
      this.saveToken(urlToken);
      // Remove token from URL
      this.removeTokenFromUrl();
      return urlToken;
    }

    // 2. Check localStorage
    const cachedToken = this.getToken();
    if (cachedToken) {
      return cachedToken;
    }

    return null;
  }

  /**
   * Extract token from URL query parameter
   */
  private static extractTokenFromUrl(): string | null {
    const params = new URLSearchParams(window.location.search);
    return params.get('token');
  }

  /**
   * Remove token from URL without page reload
   */
  private static removeTokenFromUrl(): void {
    const url = new URL(window.location.href);
    url.searchParams.delete('token');
    window.history.replaceState({}, '', url.toString());
  }

  /**
   * Save token to localStorage
   */
  static saveToken(token: string): void {
    localStorage.setItem(TOKEN_STORAGE_KEY, token);
  }

  /**
   * Get token from localStorage
   */
  static getToken(): string | null {
    return localStorage.getItem(TOKEN_STORAGE_KEY);
  }

  /**
   * Clear token from localStorage
   */
  static clearToken(): void {
    localStorage.removeItem(TOKEN_STORAGE_KEY);
  }

  /**
   * Check if token exists
   */
  static hasToken(): boolean {
    return !!this.getToken();
  }

  /**
   * Clean up legacy token keys from localStorage
   */
  private static cleanupLegacyTokens(): void {
    const legacyKeys = ['tmux_app_token', 'token'];
    legacyKeys.forEach(key => {
      if (localStorage.getItem(key)) {
        localStorage.removeItem(key);
      }
    });
  }
}
