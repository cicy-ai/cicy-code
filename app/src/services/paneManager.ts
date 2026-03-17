/**
 * Pane Selection Manager
 * Handles pane selection and caching
 */

const PANE_STORAGE_KEY = 'tmux_app_selected_pane';

export class PaneManager {
  /**
   * Get current pane ID from cache or prompt user to select
   */
  static getCurrentPane(): string | null {
    return localStorage.getItem(PANE_STORAGE_KEY);
  }

  /**
   * Set current pane ID
   */
  static setCurrentPane(paneId: string): void {
    localStorage.setItem(PANE_STORAGE_KEY, paneId);
  }

  /**
   * Clear current pane selection
   */
  static clearCurrentPane(): void {
    localStorage.removeItem(PANE_STORAGE_KEY);
  }

  /**
   * Check if pane is selected
   */
  static hasSelectedPane(): boolean {
    return !!this.getCurrentPane();
  }
}
