package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type GeminiContent struct {
	Parts []GeminiPart `json:"parts"`
}

type GeminiPart struct {
	Text string `json:"text"`
}

type GeminiRequest struct {
	Contents []GeminiContent `json:"contents"`
}

type GeminiResponse struct {
	Candidates []struct {
		Content GeminiContent `json:"content"`
	} `json:"candidates"`
}

type StudentWeeklyDataContext struct {
	StudentName         string
	ClassName           string
	WeekStartDate       string
	WeekEndDate         string
	Grades              []string
	TeacherComments     []string
	BooksRead           []string
	AttendanceInfo      string
	PreviousReportText  string
	PrevAverageGrade    float64
	CurrentAverageGrade float64
}

// GenerateAIWeeklyReport calls Google Gemini 2.5 Flash API to generate structured pedagogical weekly report
func GenerateAIWeeklyReport(data StudentWeeklyDataContext) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return generateFallbackReport(data), nil
	}

	prompt := buildGeminiPrompt(data)

	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{
				Parts: []GeminiPart{
					{Text: prompt},
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gemini request: %v", err)
	}

	url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/gemini-2.5-flash:generateContent?key=%s", apiKey)
	client := &http.Client{Timeout: 30 * time.Second}

	resp, err := client.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
	if err != nil {
		return generateFallbackReport(data), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		fmt.Printf("[Gemini API Warning] HTTP %d: %s. Using pedagogical fallback report.\n", resp.StatusCode, string(respBytes))
		return generateFallbackReport(data), nil
	}

	var geminiResp GeminiResponse
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", fmt.Errorf("failed to decode gemini response: %v", err)
	}

	if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
		return geminiResp.Candidates[0].Content.Parts[0].Text, nil
	}

	return generateFallbackReport(data), nil
}

func buildGeminiPrompt(data StudentWeeklyDataContext) string {
	hasGrades := data.CurrentAverageGrade > 0 && len(data.Grades) > 0
	hasPrevWeek := data.PrevAverageGrade > 0
	hasBooks := len(data.BooksRead) > 0

	prompt := fmt.Sprintf(`Siz 'Farzandim' maktab o'quvchilarini baholash tizimining tajribali, mehribon va samimiy Pedagogik Maslahatchi AI yordamchisiz.
Murojaatingiz ota-onaga nisbatan juda hurmatli, iliq va qo'llab-quvvatlovchi bo'lsin. Hech qanday rasmiy ravishda '0', '0.00' yoki 'yo'q' kabi sovuq iboralarni ishlatmang.

Farzand ismi: %s (%s sinf o'quvchisi).
Hafta sanasi: %s dan %s gacha.

Haftalik ko'rsatkichlar:
- O'rtacha baho: %.2f (Baholar borligi: %v)
- Baholar: %v
- O'qituvchi izohlari: %v
- O'qilgan kitoblar: %v (Kitoblar borligi: %v)
- O'tgan haftalik o'rtacha baho: %.2f (Oldingi hafta borligi: %v)

MUHIM TASHKILIY QOIDALAR:
1. Hech qanday emojilardan foydalanmang.
2. Javobingizni aniq belgilangan sarlavhalar bilan taqdim eting:

---SECTION: HAFTALIK XULOSA---
(Farzandning haftalik o'quv faoliyati va holati bo'yicha 2-3 jumlali samimiy va ilhomlantiruvchi xulosa. %s)

%s

---SECTION: FANLAR VA KITOBXONLIK TAHLILI---
(Qaysi fanlarda a'lo o'zlashtiryapti va %s)

---SECTION: OTA-ONAGA AMALIY TAVSIYA---
(Uydan turib farzandiga yordam berish bo'yicha yagona, qisqa va eng muhim bitta pedagogik maslahat. Ro'yxat qilmang, faqat 1-2 jumlada.)`,
		data.StudentName, data.ClassName, data.WeekStartDate, data.WeekEndDate,
		data.CurrentAverageGrade, hasGrades, data.Grades, data.TeacherComments,
		data.BooksRead, hasBooks, data.PrevAverageGrade, hasPrevWeek,
		func() string {
			if !hasGrades {
				return "Ushbu haftada rasmiy dars baholari qo'yilmadi yoki dam olish haftasi o'tdi deb samimiy tushuntiring."
			}
			return "Baholash ko'rsatkichlarini ijobiy yoritib bering."
		}(),
		func() string {
			if hasPrevWeek {
				return "---SECTION: DINAMIKA TAHLILI---\n(O'tgan hafta ko'rsatkichlari bilan solishtirma tahlil va o'sish dinamikasi.)"
			}
			return "" // Omit dinamika completely if no previous week data
		}(),
		func() string {
			if !hasBooks {
				return "ushbu haftada yangi kitoblar mutolaasi yakunlanmaganini, lekin mutolaaga qiziqtirish uchun yaxshi imkoniyat borligini ta'kidlang."
			}
			return fmt.Sprintf("o'qilgan kitoblar (%v) bo'yicha pedagogik tahlil.", data.BooksRead)
		}(),
	)

	return prompt
}

func generateFallbackReport(data StudentWeeklyDataContext) string {
	hasGrades := data.CurrentAverageGrade > 0
	hasPrevWeek := data.PrevAverageGrade > 0
	hasBooks := len(data.BooksRead) > 0

	var builder strings.Builder

	builder.WriteString("---SECTION: HAFTALIK XULOSA---\n")
	if hasGrades {
		builder.WriteString(fmt.Sprintf("%s joriy haftada (%s - %s) o'z o me'yorida faoliyat olib bordi. O'rtacha o'zlashtirish ko'rsatkichi %.1f ballni tashkil etdi.", data.StudentName, data.WeekStartDate, data.WeekEndDate, data.CurrentAverageGrade))
	} else {
		builder.WriteString(fmt.Sprintf("%s joriy haftada (%s - %s) dars jarayonlarida ishtirok etdi. Ushbu haftada rasmiy baholash ishlari o'tkazilmadi.", data.StudentName, data.WeekStartDate, data.WeekEndDate))
	}

	if hasPrevWeek {
		builder.WriteString("\n\n---SECTION: DINAMIKA TAHLILI---\n")
		if data.CurrentAverageGrade > data.PrevAverageGrade {
			builder.WriteString(fmt.Sprintf("Farzandingiz o'tgan haftaga nisbatan sezilarli o'sish ko'rsatib, o'rtacha bali %.1f ballga etdi.", data.CurrentAverageGrade))
		} else if data.CurrentAverageGrade < data.PrevAverageGrade {
			builder.WriteString(fmt.Sprintf("Farzandingiz ko'rsatkichi o'tgan haftaga nisbatan ozgina o'zgardi. Birgalikda takrorlash foydali bo'ladi."))
		} else {
			builder.WriteString(fmt.Sprintf("Farzandingiz barqaror natija ko'rsatib kelmoqda."))
		}
	}

	builder.WriteString("\n\n---SECTION: FANLAR VA KITOBXONLIK TAHLILI---\n")
	if hasBooks {
		builder.WriteString(fmt.Sprintf("Bu hafta o'quvchi fanlar va kitobxonlik bo'yicha faollik ko'rsatdi. O'qilgan kitoblar: %s.", strings.Join(data.BooksRead, ", ")))
	} else {
		builder.WriteString("Bu hafta o'quvchi fanlar bo'yicha bilimlarini namoyish etdi. Yangi yakunlangan kitoblar mutolaasi qayd etilmadi, uydan turib kitob o'qishga qiziqtirish tavsiya etiladi.")
	}

	builder.WriteString("\n\n---SECTION: OTA-ONAGA AMALIY TAVSIYALAR---\n")
	builder.WriteString("- Farzandingiz bilan har kuni 15 daqiqa dars muhokamasini o'tkazing.\n")
	builder.WriteString("- Farzandingizning kichik yutuqlarini ham chin dildan e'tirof etib, ruhiy qo'llab-quvvatlang.")

	return builder.String()
}
