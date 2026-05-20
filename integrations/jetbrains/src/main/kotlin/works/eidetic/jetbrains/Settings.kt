// Settings.kt — persisted application-level settings + Configurable UI panel.
//
// Maps 1:1 onto the VS Code extension's configuration keys so users have one
// mental model across editors:
//   eidetic.socketPath       → socketPath
//   eidetic.tcpHost          → tcpHost
//   eidetic.tcpPort          → tcpPort
//   eidetic.timeoutMs        → timeoutMs
//   eidetic.surfaceFilter    → surfaceFilter
//   eidetic.recentPollMs     → recentPollMs

package works.eidetic.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.openapi.options.Configurable
import com.intellij.ui.components.JBCheckBox
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBTextField
import com.intellij.util.ui.FormBuilder
import javax.swing.JComponent
import javax.swing.JPanel

/**
 * Persisted settings store. Annotation tells the platform to serialize this
 * service to `eidetic.xml` under the IDE config directory.
 */
@State(
    name = "EideticSettings",
    storages = [Storage("eidetic.xml")],
)
@Service(Service.Level.APP)
class EideticSettings : PersistentStateComponent<EideticSettings.State> {

    data class State(
        var socketPath: String = "/tmp/eidetic-daemon.sock",
        var tcpHost: String = "127.0.0.1",
        var tcpPort: Int = 9876,
        var timeoutMs: Int = 5000,
        var surfaceFilter: String = "",
        var recentPollMs: Int = 60_000,
        var forceTcp: Boolean = false,
    )

    private var myState = State()

    override fun getState(): State = myState
    override fun loadState(state: State) {
        myState = state
    }

    // Convenience accessors used by DaemonClient / UI code.
    var socketPath: String
        get() = myState.socketPath
        set(v) { myState.socketPath = v }

    var tcpHost: String
        get() = myState.tcpHost
        set(v) { myState.tcpHost = v }

    var tcpPort: Int
        get() = myState.tcpPort
        set(v) { myState.tcpPort = v }

    var timeoutMs: Int
        get() = myState.timeoutMs
        set(v) { myState.timeoutMs = v }

    var surfaceFilter: String
        get() = myState.surfaceFilter
        set(v) { myState.surfaceFilter = v }

    var recentPollMs: Int
        get() = myState.recentPollMs
        set(v) { myState.recentPollMs = v }

    var forceTcp: Boolean
        get() = myState.forceTcp
        set(v) { myState.forceTcp = v }

    companion object {
        @JvmStatic
        fun getInstance(): EideticSettings =
            ApplicationManager.getApplication().getService(EideticSettings::class.java)
    }
}

/**
 * Settings UI under Settings → Tools → Eidetic Engrams.
 * Plain Swing FormBuilder — no Kotlin DSL UI to keep deps minimal.
 */
class EideticSettingsConfigurable : Configurable {
    private var panel: JPanel? = null

    private val socketPathField = JBTextField()
    private val tcpHostField = JBTextField()
    private val tcpPortField = JBTextField()
    private val timeoutField = JBTextField()
    private val surfaceFilterField = JBTextField()
    private val recentPollField = JBTextField()
    private val forceTcpCheckbox = JBCheckBox("Force TCP transport (override OS default)")

    override fun getDisplayName(): String = "Eidetic Engrams"

    override fun createComponent(): JComponent {
        val s = EideticSettings.getInstance()
        socketPathField.text = s.socketPath
        tcpHostField.text = s.tcpHost
        tcpPortField.text = s.tcpPort.toString()
        timeoutField.text = s.timeoutMs.toString()
        surfaceFilterField.text = s.surfaceFilter
        recentPollField.text = s.recentPollMs.toString()
        forceTcpCheckbox.isSelected = s.forceTcp

        val builder = FormBuilder.createFormBuilder()
            .addLabeledComponent(JBLabel("UDS socket path (mac/linux):"), socketPathField, 1, false)
            .addLabeledComponent(JBLabel("TCP host (windows fallback):"), tcpHostField, 1, false)
            .addLabeledComponent(JBLabel("TCP port:"), tcpPortField, 1, false)
            .addLabeledComponent(JBLabel("Request timeout (ms):"), timeoutField, 1, false)
            .addLabeledComponent(JBLabel("Surface filter (empty = all):"), surfaceFilterField, 1, false)
            .addLabeledComponent(JBLabel("Recent-engrams refresh (ms):"), recentPollField, 1, false)
            .addComponent(forceTcpCheckbox, 1)
            .addComponentFillVertically(JPanel(), 0)

        panel = builder.panel
        return panel!!
    }

    override fun isModified(): Boolean {
        val s = EideticSettings.getInstance()
        return socketPathField.text != s.socketPath ||
            tcpHostField.text != s.tcpHost ||
            (tcpPortField.text.toIntOrNull() ?: -1) != s.tcpPort ||
            (timeoutField.text.toIntOrNull() ?: -1) != s.timeoutMs ||
            surfaceFilterField.text != s.surfaceFilter ||
            (recentPollField.text.toIntOrNull() ?: -1) != s.recentPollMs ||
            forceTcpCheckbox.isSelected != s.forceTcp
    }

    override fun apply() {
        val s = EideticSettings.getInstance()
        s.socketPath = socketPathField.text
        s.tcpHost = tcpHostField.text
        s.tcpPort = tcpPortField.text.toIntOrNull() ?: 9876
        s.timeoutMs = timeoutField.text.toIntOrNull() ?: 5000
        s.surfaceFilter = surfaceFilterField.text
        s.recentPollMs = recentPollField.text.toIntOrNull() ?: 60_000
        s.forceTcp = forceTcpCheckbox.isSelected
    }

    override fun reset() {
        val s = EideticSettings.getInstance()
        socketPathField.text = s.socketPath
        tcpHostField.text = s.tcpHost
        tcpPortField.text = s.tcpPort.toString()
        timeoutField.text = s.timeoutMs.toString()
        surfaceFilterField.text = s.surfaceFilter
        recentPollField.text = s.recentPollMs.toString()
        forceTcpCheckbox.isSelected = s.forceTcp
    }

    override fun disposeUIResources() {
        panel = null
    }
}
