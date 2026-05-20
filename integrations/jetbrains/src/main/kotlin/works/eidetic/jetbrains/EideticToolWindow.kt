// EideticToolWindow.kt — the per-project sidebar panel.
//
// Layout: a JBTabbedPane with three tabs:
//   1. Recent   — pollable list of last 50 engrams (cross-surface).
//   2. Surfaces — surface → count, refreshed alongside Recent.
//   3. Search   — inline search box that hits /search and lists results.
//
// All HTTP work runs on the pooled thread (DaemonClient enforces no-EDT);
// UI updates marshal back via ApplicationManager.invokeLater.

package works.eidetic.jetbrains

import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.application.ModalityState
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.ui.components.JBLabel
import com.intellij.ui.components.JBList
import com.intellij.ui.components.JBScrollPane
import com.intellij.ui.components.JBTabbedPane
import com.intellij.ui.components.JBTextField
import java.awt.BorderLayout
import java.awt.Dimension
import java.awt.FlowLayout
import javax.swing.DefaultListModel
import javax.swing.JButton
import javax.swing.JComponent
import javax.swing.JPanel
import javax.swing.ListSelectionModel
import javax.swing.SwingConstants

class EideticToolWindow(@Suppress("UNUSED_PARAMETER") project: Project) {
    private val recentModel = DefaultListModel<EngramListItem>()
    private val recentList = JBList(recentModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = EngramListRenderer()
    }

    private val surfacesModel = DefaultListModel<String>()
    private val surfacesList = JBList(surfacesModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
    }

    private val searchField = JBTextField().apply {
        toolTipText = "FTS5 query — e.g. \"postgres trick\" or bare keywords"
    }
    private val searchResultsModel = DefaultListModel<EngramListItem>()
    private val searchResultsList = JBList(searchResultsModel).apply {
        selectionMode = ListSelectionModel.SINGLE_SELECTION
        cellRenderer = EngramListRenderer()
    }

    private val statusLabel = JBLabel(" ", SwingConstants.LEFT)

    private val tabbedPane = JBTabbedPane()
    private val rootPanel: JPanel = JPanel(BorderLayout()).apply {
        preferredSize = Dimension(360, 400)
        add(tabbedPane, BorderLayout.CENTER)
        add(statusLabel, BorderLayout.SOUTH)
    }

    /** Exposed to the factory. */
    val component: JComponent get() = rootPanel

    init {
        buildRecentTab()
        buildSurfacesTab()
        buildSearchTab()

        // Initial poll (and we leave further polling to manual refresh — a
        // background timer is wired in v0.0.2 via an Alarm/ScheduledExecutor).
        refreshRecentAndSurfaces()
    }

    // ── tabs ──────────────────────────────────────────────────────────────────

    private fun buildRecentTab() {
        val panel = JPanel(BorderLayout())
        val toolbar = JPanel(FlowLayout(FlowLayout.LEFT))
        val refreshBtn = JButton("Refresh").apply {
            addActionListener { refreshRecentAndSurfaces() }
        }
        toolbar.add(refreshBtn)
        panel.add(toolbar, BorderLayout.NORTH)
        panel.add(JBScrollPane(recentList), BorderLayout.CENTER)
        tabbedPane.addTab("Recent", panel)
    }

    private fun buildSurfacesTab() {
        val panel = JPanel(BorderLayout())
        panel.add(JBScrollPane(surfacesList), BorderLayout.CENTER)
        tabbedPane.addTab("Surfaces", panel)
    }

    private fun buildSearchTab() {
        val panel = JPanel(BorderLayout())
        val top = JPanel(BorderLayout())
        val goBtn = JButton("Search").apply {
            addActionListener { runSearch(searchField.text) }
        }
        searchField.addActionListener { runSearch(searchField.text) }
        top.add(searchField, BorderLayout.CENTER)
        top.add(goBtn, BorderLayout.EAST)
        panel.add(top, BorderLayout.NORTH)
        panel.add(JBScrollPane(searchResultsList), BorderLayout.CENTER)
        tabbedPane.addTab("Search", panel)
    }

    // ── data ──────────────────────────────────────────────────────────────────

    private fun refreshRecentAndSurfaces() {
        setStatus("Loading…")
        ApplicationManager.getApplication().executeOnPooledThread {
            val client = DaemonClient.getInstance()
            val filter = EideticSettings.getInstance().surfaceFilter.ifBlank { null }
            val result = runCatching {
                val recent = client.recent(limit = 50, surface = filter)
                val surfaces = client.surfaces()
                recent to surfaces
            }
            ApplicationManager.getApplication().invokeLater({
                result.fold(
                    onSuccess = { (recent, surfaces) ->
                        recentModel.clear()
                        recent.forEach { recentModel.addElement(EngramListItem(it)) }

                        surfacesModel.clear()
                        surfaces.entries
                            .sortedByDescending { it.value }
                            .forEach { (surface, count) ->
                                surfacesModel.addElement("$surface — $count engrams")
                            }
                        setStatus("Loaded ${recent.size} recent · ${surfaces.size} surfaces")
                    },
                    onFailure = { err ->
                        setStatus("Daemon unreachable: ${err.message}")
                    }
                )
            }, ModalityState.any())
        }
    }

    private fun runSearch(rawQuery: String) {
        val query = rawQuery.trim()
        if (query.isEmpty()) return
        setStatus("Searching: $query")
        ApplicationManager.getApplication().executeOnPooledThread {
            val client = DaemonClient.getInstance()
            val filter = EideticSettings.getInstance().surfaceFilter.ifBlank { null }
            val result = runCatching { client.search(query, surface = filter, limit = 50) }
            ApplicationManager.getApplication().invokeLater({
                result.fold(
                    onSuccess = { hits ->
                        searchResultsModel.clear()
                        hits.forEach { searchResultsModel.addElement(EngramListItem(it)) }
                        setStatus("${hits.size} hits for \"$query\"")
                    },
                    onFailure = { err ->
                        Messages.showErrorDialog("Search failed: ${err.message}", "Eidetic")
                        setStatus("Search failed")
                    }
                )
            }, ModalityState.any())
        }
    }

    private fun setStatus(text: String) {
        statusLabel.text = " $text"
    }
}

/** Holder so we can stash the Engram on a list-model entry. */
data class EngramListItem(val engram: Engram) {
    override fun toString(): String =
        "[${engram.surface} #${engram.id}] " +
            DaemonClient.formatEngramTs(engram.ts) +
            " — " +
            DaemonClient.engramPreview(engram, 90)
}

/** Default renderer is fine for now — JBList uses toString(). */
private class EngramListRenderer : javax.swing.DefaultListCellRenderer()
