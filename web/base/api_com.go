package base

import (
	"errors"
	"fmt"
	"github.com/banbox/banbot/core"
	"strings"

	"github.com/banbox/banexg/errs"
	"github.com/banbox/banexg/log"
	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

var val *validator.Validate

func init() {
	val = validator.New(validator.WithRequiredStructEnabled())
}

type (
	BadField struct {
		Field string
		Tag   string
		Value interface{}
	}

	BadFields struct {
		Items []*BadField
	}
)

const (
	ArgQuery = 1
	ArgBody  = 2
)

func VerifyArg(c *fiber.Ctx, out interface{}, from int) error {
	var err error
	if from == ArgQuery {
		err = c.QueryParser(out)
	} else if from == ArgBody {
		err = c.BodyParser(out)
	} else {
		return fmt.Errorf("unsupport arg source: %v", from)
	}

	if err != nil {
		return &fiber.Error{
			Code:    fiber.StatusBadRequest,
			Message: err.Error(),
		}
	}
	if err2 := Validate(out); err2 != nil {
		return err2
	}
	return nil
}

func Validate(data interface{}) *BadFields {
	errArr := val.Struct(data)
	if errArr != nil {
		var ive *validator.InvalidValidationError
		if errors.As(errArr, &ive) {
			return &BadFields{
				Items: []*BadField{
					{
						Field: "invalid",
						Tag:   "invalid",
						Value: ive.Error(),
					},
				},
			}
		}
		var fields []*BadField
		for _, err := range errArr.(validator.ValidationErrors) {
			var elem BadField
			elem.Field = err.Field()
			elem.Tag = err.Tag()
			elem.Value = err.Value()
			fields = append(fields, &elem)
		}
		return &BadFields{Items: fields}
	}
	return nil
}

func (f *BadFields) Error() string {
	if f == nil {
		return ""
	}
	texts := make([]string, 0, len(f.Items))
	for _, it := range f.Items {
		texts = append(texts, fmt.Sprintf("[%s]: '%v', must %s", it.Field, it.Value, it.Tag))
	}
	return strings.Join(texts, ", ")
}

func ErrHandler(c *fiber.Ctx, err error) error {
	code := fiber.StatusInternalServerError
	errText := err.Error()

	var fieldErr *BadFields
	var fe *fiber.Error
	var banErr *errs.Error
	if errors.As(err, &fieldErr) {
		code = fiber.StatusBadRequest
	} else if errors.As(err, &fe) {
		code = fe.Code
	} else if errors.As(err, &banErr) {
		eCode := banErr.Code
		if eCode == errs.CodeParamInvalid || eCode == errs.CodeParamRequired || eCode == core.ErrBadConfig {
			code = fiber.StatusBadRequest
		} else if name, ok := core.ErrCodeNames[eCode]; ok && strings.HasPrefix(name, "Invalid") {
			code = fiber.StatusBadRequest
		}
		errText = banErr.Short()
	}

	fields := []zap.Field{zap.String("m", c.Method()), zap.String("url", c.OriginalURL()), zap.Error(err)}
	if code == fiber.StatusInternalServerError {
		log.Warn("server error", fields...)
	} else {
		log.Info("req fail", fields...)
	}

	c.Set(fiber.HeaderContentType, fiber.MIMETextPlainCharsetUTF8)

	return c.Status(code).SendString(errText)
}
