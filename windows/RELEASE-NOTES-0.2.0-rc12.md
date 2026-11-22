# YourLM 0.2.0-rc12 — bezpieczne odzyskiwanie pamięci urządzeń

RC12 zachowuje odporny worker Android oraz wznawiane pobieranie GGUF z RC11 i dodaje kontrolowany sposób przygotowania Windows i Androida przed uruchomieniem dużego modelu.

## Windows: lista aplikacji i łagodne zamknięcie

- Nowa karta **Odzyskaj pamięć przed modelem** pokazuje zwykłe aplikacje bieżącego użytkownika, które mają widoczne okna. Lista jest sortowana według przybliżonego RAM-u roboczego.
- Pamięć aplikacji obejmuje także rozpoznane procesy potomne, dzięki czemu przeglądarka lub edytor z wieloma procesami nie wygląda sztucznie na mały.
- Największe zwykłe aplikacje są wstępnie zaznaczone, a użytkownik może odznaczyć dowolną pozycję. Przed potwierdzeniem widzi tytuły, PID-y i przybliżoną pamięć oraz ostrzeżenie o zapisaniu pracy.
- YourLM wysyła standardowy komunikat Windows `WM_CLOSE`. Nie używa `TerminateProcess`, nie wymusza zamknięcia i nie próbuje obchodzić pytań o zapis dokumentu. Aplikacja może pokazać własne potwierdzenie i pozostać otwarta.
- Procesy systemowe, Pulpit/Eksplorator, narzędzia zabezpieczeń, instalatory i aktualizatory, terminale, sesje zdalne, programy uruchomione jako administrator oraz YourLM z silnikiem są chronione i nie mogą zostać zaznaczone.
- Przed zamknięciem lista jest skanowana ponownie. PID, nazwa procesu i status ochrony muszą nadal pasować, co ogranicza ryzyko trafienia w proces, który przejął ponownie wykorzystany PID.
- Po operacji panel ponownie mierzy aplikacje i dostępny RAM. Dla każdej wybranej pozycji pokazuje „zamknięta”, „nadal otwarta” albo „prośba niewysłana”, a obok podaje pamięć dostępną przed/po i jej przybliżoną zmianę.
- Funkcja jest zablokowana podczas aktywnej sesji modelu, aby nie zmieniać środowiska w trakcie inferencji.

## Android: bez nowych inwazyjnych uprawnień

- Ekran workera i samodzielnego hosta pokazuje kartę **Przygotuj pamięć telefonu** z bieżącą pamięcią fizyczną, zajętością systemu/aplikacji i pamięcią dostępną teraz.
- Przycisk otwiera systemową listę aplikacji Androida. Użytkownik może ręcznie zakończyć ciężką grę, aparat lub przeglądarkę, a następnie odświeżyć pomiar RAM w YourLM.
- YourLM nie żąda dostępu do statystyk użycia, Accessibility, roota ani uprawnienia do agresywnego ubijania aplikacji. Współczesny Android nie pozwala zwykłej aplikacji niezawodnie zamykać cudzych procesów, dlatego UI nie udaje, że może to zrobić.
- Komunikat ostrzega, aby nie wymuszać zatrzymania samego YourLM. Jawne zatrzymanie workera nadal jest respektowane, a ograniczone odzyskiwanie procesu natywnego z RC11 pozostaje bez zmian.

## Ważne ograniczenia

Nie da się bezpiecznie uzyskać „100% RAM-u”. Windows i Android potrzebują pamięci na jądro, sterowniki, usługi, cache plików, sieć i sam YourLM. Zamknięcie zwykłych aplikacji może zwiększyć zapas, ale pamięć dostępna teraz nadal nie jest prostą gwarancją uruchomienia konkretnego GGUF.

## Weryfikacja

CI kompiluje oba warianty hosta, wykonuje testy selekcji i ochrony procesów, sprawdza ponowne użycie PID, kompiluje Java/Android, buduje stabilnie podpisane APK i samodzielny EXE, a następnie przeprowadza instalację per-user, ochronę przed downgrade'em i odinstalowanie Windows.
