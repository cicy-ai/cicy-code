package ai.cicy.code

import com.intellij.openapi.actionSystem.ActionManager
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.DefaultActionGroup
import com.intellij.openapi.options.ShowSettingsUtil
import com.intellij.openapi.project.Project
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.ui.components.JBLabel
import com.intellij.ui.jcef.JBCefApp
import com.intellij.ui.jcef.JBCefBrowser
import javax.swing.SwingConstants

/**
 * Renders the active cicy-code team in a JCEF browser inside a right-anchored
 * tool window. Same model as the cicy-desktop Electron shell — point an
 * embedded Chromium at the team URL (+token); the backend is remote.
 */
class CicyToolWindowFactory : ToolWindowFactory {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val content = toolWindow.contentManager.factory.createContent(buildPanel(project), "", false)
        toolWindow.contentManager.addContent(content)

        val reload = object : AnAction("Reload", "Reload cicy-code", com.intellij.icons.AllIcons.Actions.Refresh) {
            override fun actionPerformed(e: AnActionEvent) {
                toolWindow.contentManager.removeAllContents(true)
                val c = toolWindow.contentManager.factory.createContent(buildPanel(project), "", false)
                toolWindow.contentManager.addContent(c)
            }
        }
        val settings = object : AnAction("Settings", "Open cicy-code settings", com.intellij.icons.AllIcons.General.Settings) {
            override fun actionPerformed(e: AnActionEvent) {
                ShowSettingsUtil.getInstance().showSettingsDialog(project, "cicy-code")
            }
        }
        (toolWindow as? com.intellij.openapi.wm.ex.ToolWindowEx)
            ?.setTitleActions(listOf(reload, settings))
    }

    private fun buildPanel(project: Project): javax.swing.JComponent {
        val src = CicySettings.getInstance().srcUrl()
        if (src.isBlank()) {
            return JBLabel(
                "<html><div style='text-align:center'>No team configured.<br/>" +
                    "Settings → Tools → cicy-code → set URL + token.</div></html>",
                SwingConstants.CENTER,
            )
        }
        if (!JBCefApp.isSupported()) {
            return JBLabel(
                "<html><div style='text-align:center'>JCEF is not available in this IDE/runtime.<br/>" +
                    "Open the team in a browser: $src</div></html>",
                SwingConstants.CENTER,
            )
        }
        val browser = JBCefBrowser(src)
        return browser.component
    }
}

// Suppress unused import warning for DefaultActionGroup/ActionManager if the
// platform pulls them transitively; kept for future toolbar extension.
@Suppress("unused")
private val keepRefs = arrayOf(DefaultActionGroup::class, ActionManager::class)
