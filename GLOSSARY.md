# Glossar

Fachbegriffe von Teamtafel. Code-Bezeichner stehen in Klammern, wenn sie vom Begriff abweichen.

## Rollen und Zugriff

**Verein**: Mandant der Plattform; alle Vereinsdaten sind per Row-Level-Security getrennt. Erreichbar unter `/<slug>/…`.

**Trainer**: Konto, das eine Mannschaft trainiert. Legt Termine an, sieht Rückmeldungen, gibt Beitrittsanfragen frei und sieht die Anwesenheitsstatistik.

**Vereinsadmin**: Konto, das einen Verein verwaltet (Mannschaften, Trainer, Vereinsfarbe, weitere Admins).

**Super-Admin** (`Superadmin`): Plattform-Admin aus der Konfiguration; legt Vereine an und darf in jedem Verein verwalten.

**Verwalter**: Trainer der Mannschaft oder Admin (Vereinsadmin oder Super-Admin). Verwalter ändern Termine und Dienste. Statistik und Beitrittsanfragen bleiben Trainern vorbehalten, Admins zählen dort nicht.

**Team** (`TeamMitglied`): Wer zur Mannschaft gehört: Verwalter, Spieler mit eigenem Konto und Eltern (Vertretungen) von Spielern im Kader. Das Team sieht die Termine der Mannschaft.

**Regel**: Was eine Route verlangt (`Konto`, `KontoAPI`, `Angemeldet`, `TeamMitglied`, `Verwalter`, `Trainer`, `Admin`, `Vereinsadmin`, `Superadmin`, oder öffentlich). `KontoAPI` antwortet ohne Login mit 401 statt zur Anmeldung zu leiten. Jede Route wird mit ihrer Regel im Zugriffsmodul registriert.

**Zugang**: Was das Zugriffsmodul für eine Anfrage aufgelöst hat: Verein, Konto, das Objekt aus dem Pfad mit seiner Mannschaft und die Rolle (Trainer, Verwalter). Handler bekommen ihn als Parameter.
