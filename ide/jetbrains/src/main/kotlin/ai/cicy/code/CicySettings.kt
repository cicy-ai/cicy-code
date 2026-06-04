package ai.cicy.code

import com.intellij.credentialStore.CredentialAttributes
import com.intellij.credentialStore.Credentials
import com.intellij.credentialStore.generateServiceName
import com.intellij.ide.passwordSafe.PasswordSafe
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage

/**
 * Holds the active cicy-code team URL (persisted) and brokers the team token
 * through the platform PasswordSafe (never written to plain XML).
 *
 * cicy-code is a SaaS: there is no local backend. The plugin only needs to know
 * which team URL to render and which token to attach.
 */
@State(name = "CicyCodeSettings", storages = [Storage("cicy-code.xml")])
class CicySettings : PersistentStateComponent<CicySettings.State> {
    data class State(var teamUrl: String = "")

    private var state = State()

    override fun getState(): State = state
    override fun loadState(s: State) { state = s }

    var teamUrl: String
        get() = state.teamUrl.trim().trimEnd('/')
        set(value) { state.teamUrl = value.trim().trimEnd('/') }

    private fun attrs(url: String): CredentialAttributes =
        CredentialAttributes(generateServiceName("cicy-code", url.ifBlank { "default" }))

    fun getToken(url: String = teamUrl): String? =
        PasswordSafe.instance.get(attrs(url))?.getPasswordAsString()

    fun setToken(url: String, token: String?) {
        PasswordSafe.instance.set(
            attrs(url),
            if (token.isNullOrBlank()) null else Credentials(url, token),
        )
    }

    /** URL with the token attached as a query param, ready for JCEF.loadURL. */
    fun srcUrl(): String {
        val base = teamUrl
        if (base.isBlank()) return ""
        val token = getToken(base) ?: return base
        val sep = if (base.contains("?")) "&" else "?"
        return base + sep + "token=" + java.net.URLEncoder.encode(token, "UTF-8")
    }

    companion object {
        fun getInstance(): CicySettings =
            ApplicationManager.getApplication().getService(CicySettings::class.java)
    }
}
