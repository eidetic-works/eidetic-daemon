// SearchAction.kt — "Tools → Eidetic: Search Engrams…" entry point.
//
// Prompts for an FTS5 query, hits /search, and offers a popup list. Picking
// a result opens the engram payload in an info dialog. Aligns with the
// VS Code extension's quickpick UX.

package works.eidetic.jetbrains

import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.application.ModalityState
import com.intellij.openapi.progress.ProgressIndicator
import com.intellij.openapi.progress.ProgressManager
import com.intellij.openapi.progress.Task
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.ui.popup.JBPopupFactory
import com.intellij.openapi.ui.popup.PopupStep
import com.intellij.openapi.ui.popup.util.BaseListPopupStep

class SearchAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project
        val query = Messages.showInputDialog(
            project,
            "Search engrams (FTS5 syntax — bare keywords or \"quoted phrase\").",
            "Eidetic: Search",
            Messages.getQuestionIcon(),
            /* initialValue = */ "",
            /* validator = */ null,
        ) ?: return
        if (query.isBlank()) return

        val task = object : Task.Backgroundable(project, "Eidetic search: $query", true) {
            override fun run(indicator: ProgressIndicator) {
                indicator.isIndeterminate = true
                val client = DaemonClient.getInstance()
                val filter = EideticSettings.getInstance().surfaceFilter.ifBlank { null }
                val result = runCatching { client.search(query, surface = filter, limit = 50) }

                ApplicationManager.getApplication().invokeLater({
                    result.fold(
                        onSuccess = { hits ->
                            if (hits.isEmpty()) {
                                Messages.showInfoMessage(project, "No engrams matched \"$query\".", "Eidetic Search")
                            } else {
                                showResultsPopup(project, query, hits)
                            }
                        },
                        onFailure = { err -> notifyError(project, "Search failed: ${err.message}") }
                    )
                }, ModalityState.any())
            }
        }
        ProgressManager.getInstance().run(task)
    }

    private fun showResultsPopup(project: Project?, query: String, hits: List<Engram>) {
        val items = hits.map { engram ->
            "[${engram.surface} #${engram.id}] " +
                DaemonClient.formatEngramTs(engram.ts) +
                " — " +
                DaemonClient.engramPreview(engram, 100)
        }
        val byLabel = items.zip(hits).toMap()

        val step = object : BaseListPopupStep<String>("${hits.size} engrams matched \"$query\"", items) {
            override fun onChosen(selectedValue: String, finalChoice: Boolean): PopupStep<*>? {
                val engram = byLabel[selectedValue]
                if (engram != null) {
                    Messages.showInfoMessage(
                        project,
                        "[${engram.surface} #${engram.id}] " +
                            DaemonClient.formatEngramTs(engram.ts) +
                            "\n\n" +
                            engram.payload,
                        "Engram #${engram.id}",
                    )
                }
                return FINAL_CHOICE
            }
        }
        JBPopupFactory.getInstance().createListPopup(step).showInFocusCenter()
    }

    private fun notifyError(project: Project?, message: String) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("Eidetic")
            .createNotification(message, NotificationType.ERROR)
            .notify(project)
    }
}
