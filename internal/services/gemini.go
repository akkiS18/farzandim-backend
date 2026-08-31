package services

import (
	"bytes"
	"database/sql"
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

type GeminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
}

type GeminiRequest struct {
	Contents         []GeminiContent         `json:"contents"`
	GenerationConfig *GeminiGenerationConfig `json:"generationConfig,omitempty"`
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

var geminiHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// GenerateAIWeeklyReport calls Google Gemini API with fallback DB query
func GenerateAIWeeklyReport(data StudentWeeklyDataContext) (string, error) {
	return GenerateAIWeeklyReportWithDB(nil, data)
}

// GenerateAIWeeklyReportWithDB loads dynamic system instructions and max tokens from DB if available
func GenerateAIWeeklyReportWithDB(dbConn *sql.DB, data StudentWeeklyDataContext) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return generateFallbackReport(data), nil
	}

	var customInstruction string
	var maxTokens int = 1000
	var temperature float64 = 0.7

	if dbConn != nil {
		_ = dbConn.QueryRow(`
			SELECT system_instruction, max_tokens, temperature 
			FROM ai_instructions 
			WHERE is_active = true 
			ORDER BY id DESC LIMIT 1
		`).Scan(&customInstruction, &maxTokens, &temperature)
	}

	prompt := buildGeminiPromptDynamic(customInstruction, data)

	reqBody := GeminiRequest{
		Contents: []GeminiContent{
			{
				Parts: []GeminiPart{
					{Text: prompt},
				},
			},
		},
		GenerationConfig: &GeminiGenerationConfig{
			MaxOutputTokens: maxTokens,
			Temperature:     temperature,
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gemini request: %v", err)
	}

	// Try models in priority order
	modelsToTry := []string{"gemini-2.5-flash", "gemini-3.6-flash", "gemini-2.0-flash", "gemini-1.5-flash"}
	var lastStatusErr string

	for _, modelName := range modelsToTry {
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent?key=%s", modelName, apiKey)

		resp, err := geminiHTTPClient.Post(url, "application/json", bytes.NewBuffer(jsonBytes))
		if err != nil {
			lastStatusErr = err.Error()
			continue
		}

		if resp.StatusCode == http.StatusOK {
			var geminiResp GeminiResponse
			if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err == nil {
				resp.Body.Close()
				if len(geminiResp.Candidates) > 0 && len(geminiResp.Candidates[0].Content.Parts) > 0 {
					return geminiResp.Candidates[0].Content.Parts[0].Text, nil
				}
			}
			resp.Body.Close()
		} else {
			respBytes, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			lastStatusErr = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(respBytes))
			fmt.Printf("[Gemini Model %s Warning] %s\n", modelName, lastStatusErr)
		}
	}

	fmt.Printf("[Gemini API Fallback] Failed calling Gemini API (%s). Using fallback template.\n", lastStatusErr)
	return generateFallbackReport(data), nil
}

func buildGeminiPromptDynamic(customInstruction string, data StudentWeeklyDataContext) string {
	if strings.TrimSpace(customInstruction) == "" {
		return buildGeminiPrompt(data)
	}

	gradesStr := "Baholar yo'q"
	if len(data.Grades) > 0 {
		gradesStr = strings.Join(data.Grades, ", ")
	}

	commentsStr := "Izohlar yo'q"
	if len(data.TeacherComments) > 0 {
		commentsStr = strings.Join(data.TeacherComments, "; ")
	}

	booksStr := "Mutolaa qilingan kitoblar yo'q"
	if len(data.BooksRead) > 0 {
		booksStr = strings.Join(data.BooksRead, ", ")
	}

	r := strings.NewReplacer(
		"{StudentName}", data.StudentName,
		"{ClassName}", data.ClassName,
		"{WeekStartDate}", data.WeekStartDate,
		"{WeekEndDate}", data.WeekEndDate,
		"{CurrentAverageGrade}", fmt.Sprintf("%.2f", data.CurrentAverageGrade),
		"{Grades}", gradesStr,
		"{TeacherComments}", commentsStr,
		"{BooksRead}", booksStr,
		"{PrevAverageGrade}", fmt.Sprintf("%.2f", data.PrevAverageGrade),
	)

	return r.Replace(customInstruction)
}

func buildGeminiPrompt(data StudentWeeklyDataContext) string {
	hasGrades := data.CurrentAverageGrade > 0 && len(data.Grades) > 0
	hasPrevWeek := data.PrevAverageGrade > 0
	hasBooks := len(data.BooksRead) > 0

	prompt := fmt.Sprintf(`Siz 'Farzandim' tizimining g'amxo'r va tajribali ustozisiz. Ota-onaga ularning farzandi haqida xuddi maktabda yuzma-yuz suhbatlashayotgandek, juda samimiy va jonli tilda xat yozing. 
Sizning vazifangiz quruq raqamlar va baholarni sanab o'tish emas, balki bu baholar va o'qilgan kitoblar ortidagi mehnatni tahlil qilib, ota-onaga tushunarli tilda yetkazishdir. "Robot" yoki "avtomatlashtirilgan tizim" kabi taassurot qoldirmang.

Farzand ismi: %s (%s sinf o'quvchisi).
Hafta sanasi: %s dan %s gacha.

Haftalik ko'rsatkichlar:
- O'rtacha baho: %.2f (Baholar borligi: %v)
- Baholar: %v
- O'qituvchi izohlari: %v
- O'qilgan kitoblar: %v (Kitoblar borligi: %v)
- O'tgan haftalik o'rtacha baho: %.2f (Oldingi hafta borligi: %v)

MUHIM QOIDALAR:
1. Hech qanday emojilardan foydalanmang.
2. Murakkab atamalarsiz, ota-ona qalbini isitadigan va to'g'ri yo'l ko'rsatadigan tildan foydalaning.

Javobingizni quyidagi qismlarga ajrating:

---SECTION: HAFTALIK XULOSA---
(Farzandning joriy haftadagi umumiy holati va kayfiyati haqida 2-3 jumlali samimiy kirish so'zi. Agar baholar bo'lmasa, buni tabiiy ravishda tushuntiring: %s)

%s

---SECTION: FANLAR VA KITOBXONLIK TAHLILI---
(O'quvchining baholari va o'qigan kitoblarini o'zaro bog'lab, butunlay jonli matn ko'rinishida tahlil qiling. Masalan: "Uning adabiyotdan olgan baholari va o'qiyotgan kitoblari fikrlash doirasi kengayayotganini ko'rsatadi". Ro'yxat qilmang, faqat chiroyli gaplar bilan tushuntiring. %s)

---SECTION: OTA-ONAGA AMALIY TAVSIYA---
(Shu haftadagi natijalarga asoslanib, ota-ona dam olish kunlari farzandi bilan nima qilishi kerakligi haqida faqat bitta, lekin juda foydali amaliy maslahat bering).`,
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
			return ""
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
		builder.WriteString(fmt.Sprintf("%s joriy haftada (%s - %s) o'z o'rnida harakat qildi. O'rtacha o'zlashtirish ko'rsatkichi %.1f ballni tashkil etdi.", data.StudentName, data.WeekStartDate, data.WeekEndDate, data.CurrentAverageGrade))
	} else {
		builder.WriteString(fmt.Sprintf("%s joriy haftada (%s - %s) dars jarayonlarida ishtirok etdi. Ushbu haftada baholash ishlari o'tkazilmadi.", data.StudentName, data.WeekStartDate, data.WeekEndDate))
	}

	if hasPrevWeek {
		builder.WriteString("\n\n---SECTION: DINAMIKA TAHLILI---\n")
		if data.CurrentAverageGrade > data.PrevAverageGrade {
			builder.WriteString(fmt.Sprintf("Farzandingiz o'tgan haftaga nisbatan yaxshi o'sish ko'rsatib, o'rtacha bali %.1f ballga etdi.", data.CurrentAverageGrade))
		} else if data.CurrentAverageGrade < data.PrevAverageGrade {
			builder.WriteString("Farzandingiz ko'rsatkichi o'tgan haftaga nisbatan biroz pasaydi. Ba'zi mavzularda qo'shimcha yordam kerak bo'lishi mumkin.")
		} else {
			builder.WriteString("Farzandingiz barqaror natija ko'rsatib kelmoqda.")
		}
	}

	builder.WriteString("\n\n---SECTION: FANLAR VA KITOBXONLIK TAHLILI---\n")
	if hasBooks {
		builder.WriteString(fmt.Sprintf("Bu hafta o'quvchi mutolaaga vaqt ajratdi. O'qilgan kitoblar: %s. Kitobxonlik tafakkurni kengaytirishga katta xizmat qiladi.", strings.Join(data.BooksRead, ", ")))
	} else {
		builder.WriteString("Bu hafta davomida fanlar bo'yicha o'z bilimlarini namoyish etdi. Bo'sh vaqtlarda yangi kitoblar mutolaasiga qiziqtirish foydali bo'ladi.")
	}

	builder.WriteString("\n\n---SECTION: OTA-ONAGA AMALIY TAVSIYA---\n")
	builder.WriteString("Farzandingizning har qanday kichik yutug'ini e'tirof eting va uni kelgusi haftada yangi bilimlarni o'zlashtirishga ruhlantiring.")

	return builder.String()
}
