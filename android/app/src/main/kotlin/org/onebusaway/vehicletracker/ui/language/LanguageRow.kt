package org.onebusaway.vehicletracker.ui.language

import android.os.Build
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.AlertDialog
import androidx.compose.material3.Icon
import androidx.compose.material3.ListItem
import androidx.compose.material3.ListItemDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.res.painterResource
import androidx.compose.ui.res.stringResource
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.AnnotatedString
import androidx.compose.ui.text.SpanStyle
import androidx.compose.ui.text.buildAnnotatedString
import androidx.compose.ui.text.intl.LocaleList
import androidx.compose.ui.text.withStyle
import androidx.compose.ui.unit.dp
import org.onebusaway.vehicletracker.R
import org.onebusaway.vehicletracker.ui.components.GroupOuterCorner
import org.onebusaway.vehicletracker.ui.components.QuietButton

private val RowShape = RoundedCornerShape(GroupOuterCorner)

/**
 * The Language row on the first welcome screen: "Language: English", in the language the app shows. [enabled] false takes
 * no taps, for a welcome screen not shown.
 */
@Composable
internal fun LanguageRow(modifier: Modifier = Modifier, enabled: Boolean = true) {
    LanguageChooser { shown, opensPhoneSettings, onClick ->
        LanguageRowContent(shown, opensPhoneSettings, enabled, modifier, onClick)
    }
}

/**
 * A row that says which language the app shows, however [row] draws it, and what a tap on it does: it opens the list of
 * the app's languages on Android 13 and up, and the phone's language settings below.
 */
@Composable
internal fun LanguageChooser(row: @Composable (shown: AppLanguage, opensPhoneSettings: Boolean, onClick: () -> Unit) -> Unit) {
    val context = LocalContext.current
    val available = availableLanguages()
    val shown = shownLanguage(preferredLocales(), available)
    val controller = remember(context) { LanguageRowController(Build.VERSION.SDK_INT, AndroidLanguageSettings(context)) }
    var listOpen by rememberSaveable { mutableStateOf(false) }

    row(appLanguage(shown), controller.opensPhoneSettings) {
        if (controller.tap()) listOpen = true
    }
    if (listOpen) {
        LanguageList(
            languages = languageChoices(available, shown),
            shownTag = shown.toLanguageTag(),
            onChoose = { tag ->
                listOpen = false
                controller.choose(tag, shown.toLanguageTag())
            },
            onDismiss = { listOpen = false },
        )
    }
}

/** The row itself, which TalkBack reads as one button: the language, then below Android 13 where it is set. */
@Composable
internal fun LanguageRowContent(shown: AppLanguage, opensPhoneSettings: Boolean, enabled: Boolean, modifier: Modifier = Modifier, onClick: () -> Unit) {
    val colors = MaterialTheme.colorScheme
    ListItem(
        headlineContent = { Text(stringResource(R.string.language_row, shown.ownName)) },
        supportingContent = if (opensPhoneSettings) {
            { Text(stringResource(R.string.language_row_phone_note)) }
        } else {
            null
        },
        leadingContent = { Icon(painterResource(R.drawable.ic_language), contentDescription = null) },
        trailingContent = { Icon(painterResource(R.drawable.ic_chevron_right), contentDescription = null) },
        colors = ListItemDefaults.colors(containerColor = colors.surfaceContainer),
        modifier = modifier.clip(RowShape).clickable(enabled = enabled, role = Role.Button, onClick = onClick),
    )
}

/**
 * The app's languages, one to a row: its own name, which TalkBack reads in that language, over its English name. A tap
 * on one chooses it and closes the list.
 */
@Composable
internal fun LanguageList(languages: List<AppLanguage>, shownTag: String, onChoose: (String) -> Unit, onDismiss: () -> Unit) {
    AlertDialog(
        onDismissRequest = onDismiss,
        icon = { Icon(painterResource(R.drawable.ic_language), contentDescription = null) },
        iconContentColor = MaterialTheme.colorScheme.onSurfaceVariant,
        title = { Text(stringResource(R.string.language_list_title)) },
        text = {
            Column(Modifier.selectableGroup().verticalScroll(rememberScrollState())) {
                for (language in languages) {
                    LanguageChoice(language, selected = language.tag == shownTag) { onChoose(language.tag) }
                }
            }
        },
        confirmButton = { QuietButton(stringResource(R.string.language_list_cancel), onClick = onDismiss) },
    )
}

@Composable
private fun LanguageChoice(language: AppLanguage, selected: Boolean, onClick: () -> Unit) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .heightIn(min = 56.dp)
            .selectable(selected = selected, role = Role.RadioButton, onClick = onClick),
        verticalAlignment = Alignment.CenterVertically,
    ) {
        RadioButton(selected = selected, onClick = null)
        Column(Modifier.padding(start = 16.dp)) {
            Text(inItsLanguage(language), style = MaterialTheme.typography.bodyLarge)
            language.englishName?.let {
                Text(it, style = MaterialTheme.typography.bodyMedium, color = MaterialTheme.colorScheme.onSurfaceVariant)
            }
        }
    }
}

/** The language's own name, marked as written in it: TalkBack reads it with that language's voice. */
private fun inItsLanguage(language: AppLanguage): AnnotatedString = buildAnnotatedString {
    withStyle(SpanStyle(localeList = LocaleList(language.tag))) { append(language.ownName) }
}
