package org.onebusaway.vehicletracker.ui.settings

import org.junit.Assert.assertEquals
import org.junit.Assert.assertTrue
import org.junit.Test
import java.io.File

/** The license list against what the app is built from, and the texts it shows. */
class LicensesTest {
    @Test fun `every license's full text is in the app`() {
        for (license in Licenses) {
            val text = File("src/main/assets/${license.text}")
            assertTrue("${license.name}: ${license.text}", text.isFile && text.length() > 0)
        }
    }

    @Test fun `the list is by name`() {
        assertEquals(Licenses.map { it.name }.sortedBy { it.lowercase() }, Licenses.map { it.name })
    }

    /** As build.gradle.kts declares them, through the version catalog: the release build's own libraries. */
    private fun declaredGroups(): Set<String> {
        val catalog = File("../gradle/libs.versions.toml").readText()
        val aliases = Regex("""^\s*(implementation|api|runtimeOnly)\((?:platform\()?libs\.([\w.]+)\)""", RegexOption.MULTILINE)
            .findAll(File("build.gradle.kts").readText()).map { it.groupValues[2].replace('.', '-') }.toList()
        assertTrue("read the build file", aliases.size > 10)
        return aliases.map { alias ->
            Regex("""^$alias\s*=\s*\{\s*group\s*=\s*"([^"]+)"""", RegexOption.MULTILINE).find(catalog)?.groupValues?.get(1)
                ?: error("$alias is not in the catalog")
        }.toSet()
    }

    @Test fun `every library the app is built with has a row`() {
        val covered = Licenses.flatMap { it.groups }
        for (group in declaredGroups()) {
            assertTrue("$group has no row", covered.any { group == it || group.startsWith("$it.") })
        }
    }

    @Test fun `a license's lines are joined again where they were only kept short`() {
        val text = "Permission is hereby granted, free of charge, to any person obtaining\na copy of this software.\n\n\nTHE SOFTWARE IS PROVIDED \"AS IS\"."

        assertEquals(
            listOf("Permission is hereby granted, free of charge, to any person obtaining a copy of this software.", "THE SOFTWARE IS PROVIDED \"AS IS\"."),
            paragraphsOf(text),
        )
    }

    @Test fun `list items and headings keep a line of their own`() {
        val text = "Conditions, which every copy and every binary that is given on must keep:\n* Redistributions of source code must retain the above copyright\n  notice, this list of conditions.\n* Binaries must reproduce it.\n1. Definitions.\n" +
            "(a) You must give any other recipients of the Work or Derivative Works\na copy of this License.\n=====\n# Licenses"

        assertEquals(
            listOf(
                "Conditions, which every copy and every binary that is given on must keep:\n* Redistributions of source code must retain the above copyright notice, this list of conditions.\n" +
                    "* Binaries must reproduce it.\n1. Definitions.\n(a) You must give any other recipients of the Work or Derivative Works a copy of this License.\n=====\n# Licenses",
            ),
            paragraphsOf(text),
        )
    }

    // As the font license's own heading and its first word stand.
    @Test fun `a short line and a rule keep their breaks`() {
        val text = "-------------------------------------------------------------------\nSIL OPEN FONT LICENSE Version 1.1 - 26 February 2007\n" +
            "-------------------------------------------------------------------\nPREAMBLE\nThe goals of the Open Font License (OFL) are to stimulate worldwide"

        assertEquals(listOf(text), paragraphsOf(text))
    }

    @Test fun `windows line ends and blank lines with spaces part paragraphs too`() {
        assertEquals(
            listOf("Each line of this first paragraph is long enough to be wrapped one", "three"),
            paragraphsOf("Each line of this first paragraph is long enough to be\r\nwrapped one\r\n   \r\nthree\r\n"),
        )
    }
}
