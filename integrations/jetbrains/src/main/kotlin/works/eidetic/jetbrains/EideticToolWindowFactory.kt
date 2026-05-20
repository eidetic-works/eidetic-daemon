// EideticToolWindowFactory.kt — registers the "Eidetic" tool window with
// the platform. Each open project gets its own EideticToolWindow instance.

package works.eidetic.jetbrains

import com.intellij.openapi.project.Project
import com.intellij.openapi.wm.ToolWindow
import com.intellij.openapi.wm.ToolWindowFactory
import com.intellij.ui.content.ContentFactory

class EideticToolWindowFactory : ToolWindowFactory {
    override fun createToolWindowContent(project: Project, toolWindow: ToolWindow) {
        val panel = EideticToolWindow(project)
        val content = ContentFactory.getInstance().createContent(
            panel.component,
            /* displayName = */ "",
            /* isLockable = */ false,
        )
        toolWindow.contentManager.addContent(content)
    }

    override fun shouldBeAvailable(project: Project): Boolean = true
}
