package ai.cicy.code

import com.intellij.openapi.options.Configurable
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBPasswordField
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import javax.swing.JComponent
import javax.swing.JPanel

/** Settings → Tools → cicy-code: configure the active team URL + token. */
class CicyConfigurable : Configurable {
    private val urlField = JBTextField()
    private val tokenField = JBPasswordField()
    private var panel: JPanel? = null

    override fun getDisplayName(): String = "cicy-code"

    override fun createComponent(): JComponent {
        val s = CicySettings.getInstance()
        urlField.text = s.teamUrl
        tokenField.text = s.getToken().orEmpty()
        panel = FormBuilder.createFormBuilder()
            .addLabeledComponent("Team URL:", urlField, 1, false)
            .addLabeledComponent("API token:", tokenField, 1, false)
            .addComponent(JBLabel("<html><small>cicy-code is a SaaS. Create/join a team, then paste its URL + token.</small></html>"))
            .addComponentFillVertically(JPanel(), 0)
            .panel
        return panel!!
    }

    override fun isModified(): Boolean {
        val s = CicySettings.getInstance()
        return urlField.text.trim().trimEnd('/') != s.teamUrl ||
            String(tokenField.password) != s.getToken().orEmpty()
    }

    override fun apply() {
        val s = CicySettings.getInstance()
        val url = urlField.text.trim().trimEnd('/')
        s.teamUrl = url
        s.setToken(url, String(tokenField.password))
    }

    override fun reset() {
        val s = CicySettings.getInstance()
        urlField.text = s.teamUrl
        tokenField.text = s.getToken().orEmpty()
    }

    override fun disposeUIResources() { panel = null }
}
