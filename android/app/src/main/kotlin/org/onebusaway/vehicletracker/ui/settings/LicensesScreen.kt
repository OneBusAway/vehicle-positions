@file:OptIn(ExperimentalMaterial3Api::class)

package org.onebusaway.vehicletracker.ui.settings

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.lazy.itemsIndexed
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.material3.TopAppBarDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.produceState
import androidx.compose.ui.Modifier
import androidx.compose.ui.input.nestedscroll.nestedScroll
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.unit.dp
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.withContext
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.groupPlace
import org.onebusaway.vehicletracker.ui.components.groupRow

/** Each library and font in the app with its license's name. A tap shows the license in full. */
@Composable
fun LicensesScreen(onBack: () -> Unit, onOpen: (index: Int) -> Unit) {
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        SettingsPageBar(R.string.licenses_title, onBack, scrollBehavior)
        LazyColumn(contentPadding = PaddingValues(start = 16.dp, top = 8.dp, end = 16.dp, bottom = 32.dp)) {
            itemsIndexed(Licenses) { index, license ->
                ListItem(
                    headlineContent = { Text(license.name) },
                    supportingContent = { Text(license.license) },
                    trailingContent = { Icon(painterResource(R.drawable.ic_chevron_right), contentDescription = null) },
                    colors = ListItemDefaults.colors(containerColor = MaterialTheme.colorScheme.surfaceContainer),
                    modifier = Modifier.groupRow(groupPlace(index, Licenses.size)).clickable(role = Role.Button) { onOpen(index) },
                )
            }
        }
    }
}

/**
 * The license of [Licenses] at [index], in full, read from the app's assets. Paragraph by paragraph, as one is long: the
 * Play services notices run to hundreds of pages.
 */
@Composable
fun LicenseScreen(index: Int, onBack: () -> Unit) {
    val license = Licenses[index]
    val context = LocalContext.current
    val paragraphs by produceState(emptyList<String>(), license) {
        value = withContext(Dispatchers.IO) { paragraphsOf(context.assets.open(license.text).bufferedReader().use { it.readText() }) }
    }
    val scrollBehavior = TopAppBarDefaults.pinnedScrollBehavior()
    Column(Modifier.fillMaxSize().nestedScroll(scrollBehavior.nestedScrollConnection)) {
        SettingsPageBar(license.name, onBack, scrollBehavior)
        LazyColumn(contentPadding = PaddingValues(start = 20.dp, top = 8.dp, end = 20.dp, bottom = 32.dp)) {
            items(paragraphs) { paragraph ->
                Text(paragraph, style = MaterialTheme.typography.bodyMedium, modifier = Modifier.padding(bottom = 12.dp))
            }
        }
    }
}

/**
 * [text] cut at its blank lines. A paragraph's lines are joined again where its file broke them only to keep them short,
 * so they wrap to the screen. A line stays on its own after a short one, which was broken there on purpose, after a
 * rule, and when it starts a list item, a heading or a rule.
 */
internal fun paragraphsOf(text: String): List<String> =
    text.replace("\r\n", "\n").split(Regex("\n[ \t]*\n+")).filter { it.isNotBlank() }.map(::unwrapped)

/** Shorter than this, a line was broken where it ends on purpose: the texts are kept to about 70 a line. */
private const val WRAPPED_LINE = 50

private val StartsItsOwnLine = Regex("""^\s*(([-*+#]|\(?[0-9a-zA-Z]{1,3}[.)])\s|={3,}|-{3,})""")
private val Rule = Regex("""^\s*(={3,}|-{3,})\s*$""")

private fun unwrapped(paragraph: String): String {
    val lines = paragraph.lines().map { it.trimEnd() }.filter { it.isNotEmpty() }
    return lines.zipWithNext().fold(StringBuilder(lines.first().trim())) { joined, (before, line) ->
        val continues = before.length >= WRAPPED_LINE && !Rule.matches(before) && !StartsItsOwnLine.containsMatchIn(line)
        joined.append(if (continues) ' ' else '\n').append(line.trim())
    }.toString()
}
