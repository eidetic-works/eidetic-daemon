// RecallAction.kt — "Tools → Eidetic: Recall…" entry point.
//
// Prompts the user for a question, hits /ask on the daemon, and displays the
// grounding instructions + engram citations in an info dialog. (Future: render
// in a dedicated tool-window tab with copy-citation buttons.)

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
import com.intellij.openapi.ui.Messages

class RecallAction : AnAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project
        val question = Messages.showInputDialog(
            project,
            "Ask Eidetic a question — it will retrieve matching engrams with citations.",
            "Eidetic: Recall",
            Messages.getQuestionIcon(),
            /* initialValue = */ "",
            /* validator = */ null,
        ) ?: return
        if (question.isBlank()) return

        val task = object : Task.Backgroundable(project, "Eidetic recall: $question", true) {
            override fun run(indicator: ProgressIndicator) {
                indicator.isIndeterminate = true
                val client = DaemonClient.getInstance()
                val filter = EideticSettings.getInstance().surfaceFilter.ifBlank { null }
                val result = runCatching { client.ask(question, surface = filter, limit = 10) }

                ApplicationManager.getApplication().invokeLater({
                    result.fold(
                        onSuccess = { ask -> showAskResult(project, ask) },
                        onFailure = { err -> notifyError(project, "Recall failed: ${err.message}") }
                    )
                }, ModalityState.any())
            }
        }
        ProgressManager.getInstance().run(task)
    }

    private fun showAskResult(project: com.intellij.openapi.project.Project?, ask: AskResponse) {
        val body = buildString {
            append("Question: ").append(ask.question).append("\n")
            append("FTS query: ").append(ask.fts_query).append("\n\n")
            if (ask.instructions.isNotBlank()) {
                append("Instructions:\n").append(ask.instructions).append("\n\n")
            }
            append("Engrams (${ask.engrams.size}):\n")
            ask.engrams.forEachIndexed { idx, engram ->
                append("\n#${idx + 1} [${engram.surface} #${engram.id}] ")
                append(DaemonClient.formatEngramTs(engram.ts)).append("\n")
                append(DaemonClient.engramPreview(engram, 300)).append("\n")
            }
        }
        // For 0.0.1, a long-message dialog is sufficient; v0.0.2 swaps to a
        // dedicated panel rendered into the tool window.
        Messages.showInfoMessage(project, body, "Eidetic Recall")
    }

    private fun notifyError(project: com.intellij.openapi.project.Project?, message: String) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("Eidetic")
            .createNotification(message, NotificationType.ERROR)
            .notify(project)
    }
}
